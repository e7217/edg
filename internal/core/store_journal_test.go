package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The journal mode decides whether a read and a write can happen at the same
// time. Under SQLite's rollback journal they cannot: a reader's shared lock
// blocks the writer, and `_busy_timeout` only makes the loser wait. WAL lets
// readers run while the writer commits.
//
// TestJournalModeContention measures that rather than assuming it. It is
// skipped unless EDG_BENCH=1 because it runs for seconds, not milliseconds:
//
//	EDG_BENCH=1 go test ./internal/core -run TestJournalModeContention -v
//
// The mix is what the gateway actually does while it runs: per-message asset
// lookups and ancestor traversals on the ingest path, point-list reads that
// span several statements, and provisioning writes.

func newFileStore(t *testing.T, mode string) *Store {
	return newFileStoreSync(t, mode, "")
}

func newFileStoreSync(t *testing.T, mode, sync string) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStoreWithOptions(filepath.Join(dir, "metadata.db"), StoreOptions{
		AutoMigrate: true, JournalMode: mode, Synchronous: sync,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// A file-backed test, because PRAGMA journal_mode is a no-op on :memory: --
// the whole suite would otherwise run in a mode production never uses.
func TestJournalModeTakesEffect(t *testing.T) {
	for _, mode := range []string{JournalModeWAL, JournalModeDelete} {
		store := newFileStore(t, mode)
		got, err := store.JournalMode()
		require.NoError(t, err)
		assert.Equal(t, mode, got, "DSN asked for %s", mode)
	}

	mem, err := NewStoreWithOptions(":memory:", StoreOptions{AutoMigrate: true, JournalMode: JournalModeWAL})
	require.NoError(t, err)
	defer mem.Close()
	got, err := mem.JournalMode()
	require.NoError(t, err)
	assert.NotEqual(t, JournalModeWAL, got,
		"a memory database cannot be WAL; an in-memory test cannot speak for production")
}

// WAL keeps its own -wal and -shm files beside the database. Anything that
// copies metadata.db alone -- the backup advice in the user guide used to --
// would capture a database missing its most recent commits.
func TestWALLeavesSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.db")
	store, err := NewStoreWithOptions(path, StoreOptions{AutoMigrate: true, JournalMode: JournalModeWAL})
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.CreateAsset(&Asset{ID: "a", Name: "A", Source: SourceManual}))

	names, err := filepath.Glob(path + "*")
	require.NoError(t, err)
	var suffixes []string
	for _, n := range names {
		suffixes = append(suffixes, strings.TrimPrefix(filepath.Base(n), "metadata.db"))
	}
	sort.Strings(suffixes)
	assert.Contains(t, suffixes, "-wal")
}

type latencies struct {
	mu sync.Mutex
	v  []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.v = append(l.v, d)
	l.mu.Unlock()
}

func (l *latencies) quantiles() (p50, p95, p99, max time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.v) == 0 {
		return
	}
	sort.Slice(l.v, func(i, j int) bool { return l.v[i] < l.v[j] })
	q := func(p float64) time.Duration { return l.v[int(float64(len(l.v)-1)*p)] }
	return q(0.50), q(0.95), q(0.99), l.v[len(l.v)-1]
}

func (l *latencies) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.v)
}

func TestJournalModeContention(t *testing.T) {
	if os.Getenv("EDG_BENCH") != "1" {
		t.Skip("set EDG_BENCH=1 to run the journal-mode measurement")
	}
	const (
		assets   = 200
		readers  = 8
		writers  = 2
		duration = 10 * time.Second
	)

	configs := []struct{ mode, sync string }{
		{JournalModeDelete, "full"}, // today's default
		{JournalModeWAL, "full"},    // same durability, different concurrency
		{JournalModeWAL, "normal"},  // one fsync fewer per commit
	}
	for _, cfg := range configs {
		mode := cfg.mode + "/" + cfg.sync
		store := newFileStoreSync(t, cfg.mode, cfg.sync)
		svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

		// A plant: assets in a two-level hierarchy, each with a point list,
		// so reads traverse relations and span several statements.
		require.NoError(t, store.CreateAsset(&Asset{ID: "line-1", Name: "Line 1", Source: SourceManual}))
		for i := 0; i < assets; i++ {
			id := fmt.Sprintf("pump-%03d", i)
			require.NoError(t, store.CreateAsset(&Asset{ID: id, Name: "Pump " + id, Source: SourceManual}))
			require.NoError(t, store.CreateRelation(&AssetRelation{
				ID: "r-" + id, SourceAssetID: id, TargetAssetID: "line-1",
				RelationType: RelationPartOf, CreatedAt: time.Now(),
			}))
			_, err := store.UpsertPointList(&PointList{AssetID: id, Protocol: "modbus-tcp", Points: []Point{
				{Name: "temperature", ValueType: ValueTypeNumber, Address: "0", Enabled: true},
				{Name: "pressure", ValueType: ValueTypeNumber, Address: "1", Enabled: true},
			}})
			require.NoError(t, err)
		}

		var readLat, writeLat latencies
		var readErrs, writeErrs atomic.Int64
		stop := make(chan struct{})
		var wg sync.WaitGroup

		for r := 0; r < readers; r++ {
			wg.Add(1)
			go func(r int) {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					id := fmt.Sprintf("pump-%03d", (r*37+i)%assets)
					start := time.Now()
					var err error
					switch i % 3 {
					case 0: // the ingest path's per-message lookup
						_, err = store.AssetExists(id)
					case 1: // enrichment on a cache miss
						_, err = store.GetAncestors(id, DefaultHierarchicalRelationTypes(), 5)
					default: // a multi-statement read
						_, err = store.GetPointList(id)
					}
					if err != nil {
						readErrs.Add(1)
					}
					readLat.add(time.Since(start))
				}
			}(r)
		}

		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					id := fmt.Sprintf("pump-%03d", (w*53+i)%assets)
					start := time.Now()
					_, err := svc.UpsertPointList(UpsertPointListRequest{
						AssetID: id, Protocol: "modbus-tcp", Points: []Point{
							{Name: "temperature", ValueType: ValueTypeNumber, Address: "0", Enabled: true},
							{Name: "pressure", ValueType: ValueTypeNumber, Address: fmt.Sprint(i % 9), Enabled: true},
						},
					})
					if err != nil {
						writeErrs.Add(1)
					}
					writeLat.add(time.Since(start))
				}
			}(w)
		}

		time.Sleep(duration)
		close(stop)
		wg.Wait()

		rp50, rp95, rp99, rmax := readLat.quantiles()
		wp50, wp95, wp99, wmax := writeLat.quantiles()
		secs := duration.Seconds()
		t.Logf("%-6s reads  %6.0f/s  p50 %8s p95 %8s p99 %8s max %8s errors %d",
			mode, float64(readLat.count())/secs, rp50, rp95, rp99, rmax, readErrs.Load())
		t.Logf("%-6s writes %6.0f/s  p50 %8s p95 %8s p99 %8s max %8s errors %d",
			mode, float64(writeLat.count())/secs, wp50, wp95, wp99, wmax, writeErrs.Load())
	}
}
