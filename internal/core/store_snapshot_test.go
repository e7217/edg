package core

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dry run must see what a running core has committed. In WAL mode a commit
// sits in the -wal file until a checkpoint, so copying the main file alone
// misses it; SnapshotTo does not.
func TestSnapshotIncludesCommitsStillInTheWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metadata.db")
	walStore := func(path string) (*Store, error) {
		return NewStoreWithOptions(path, StoreOptions{AutoMigrate: true, JournalMode: JournalModeWAL})
	}

	running, err := walStore(dbPath) // edg-core, holding the database open
	require.NoError(t, err)
	t.Cleanup(func() { _ = running.Close() })
	require.NoError(t, running.CreateAsset(&Asset{ID: "pump-a", Name: "A", Source: SourceManual}))
	wal, err := os.Stat(dbPath + "-wal")
	require.NoError(t, err)
	require.Positive(t, wal.Size(), "the commit is still in the WAL")

	copied := filepath.Join(dir, "copied.db")
	copyPlain(t, dbPath, copied)
	fromCopy, err := NewStore(copied)
	require.NoError(t, err)
	t.Cleanup(func() { _ = fromCopy.Close() })
	lost, err := fromCopy.GetAsset("pump-a")
	require.NoError(t, err)
	assert.Nil(t, lost, "a plain file copy loses the uncheckpointed commit")

	cli, err := walStore(dbPath) // the dry run's own connection
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	snap := filepath.Join(dir, "snapshot.db")
	require.NoError(t, cli.SnapshotTo(snap))
	fromSnap, err := NewStore(snap)
	require.NoError(t, err)
	t.Cleanup(func() { _ = fromSnap.Close() })
	got, err := fromSnap.GetAsset("pump-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "A", got.Name)
}

func TestSnapshotRefusesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "m.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	existing := filepath.Join(dir, "taken.db")
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0o600))
	assert.Error(t, s.SnapshotTo(existing))
}

func copyPlain(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	require.NoError(t, err)
	defer in.Close()
	out, err := os.Create(dst)
	require.NoError(t, err)
	_, err = io.Copy(out, in)
	require.NoError(t, err)
	require.NoError(t, out.Close())
}
