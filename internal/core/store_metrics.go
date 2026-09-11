package core

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/e7217/edg/internal/metrics"
)

// storeQueryFailures counts SQL errors on the read paths the data plane
// depends on. Until now a rising SQLite error rate was visible only in the
// log.
var storeQueryFailures = metrics.Default.NewCounter(metrics.Desc{
	Name: "edg_core_store_query_failures_total",
	Help: "Metadata queries that returned an error on the asset-count path.",
})

// PoolStats exposes the database/sql pool counters.
//
// It exists so that internal/metrics never has to see a *sql.DB: the metrics
// package stays a pure encoder with no knowledge of what it is describing.
func (s *Store) PoolStats() sql.DBStats {
	if s == nil || s.db == nil {
		return sql.DBStats{}
	}
	return s.db.Stats()
}

// CountRelations returns the number of asset relations.
func (s *Store) CountRelations() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM asset_relations`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// StoreMetricsOptions tunes the cached master-data counts.
type StoreMetricsOptions struct {
	// TTL is how long a count is reused. Zero means DefaultStoreCountTTL.
	TTL time.Duration
	// Timeout bounds one refresh. Zero means DefaultStoreCountTimeout.
	Timeout time.Duration
}

const (
	// DefaultStoreCountTTL is generous on purpose: master data changes when an
	// operator changes it, not on the data path, and each refresh is two
	// SELECT COUNT(*) against the same serialized SQLite handle the ingest
	// path uses.
	DefaultStoreCountTTL = time.Minute
	// DefaultStoreCountTimeout bounds one refresh so a locked database cannot
	// hold a scrape open.
	DefaultStoreCountTimeout = 2 * time.Second
)

// RegisterMetrics exposes this store's pool and master-data counts.
//
// It is a method rather than a package-level declaration because the numbers
// describe one database, and the test suite opens many; cmd/core calls it once
// for the production store.
func (s *Store) RegisterMetrics(r *metrics.Registry, opts StoreMetricsOptions) {
	if s == nil {
		return
	}

	// Pool stats are in-memory counters on *sql.DB, so they are safe to read
	// at scrape time.
	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_store_connections_open",
		Help: "Open connections to the metadata database.",
	}, func() float64 { return float64(s.PoolStats().OpenConnections) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_store_connections_in_use",
		Help: "Connections currently executing a query.",
	}, func() float64 { return float64(s.PoolStats().InUse) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_store_connections_idle",
		Help: "Connections sitting idle in the pool.",
	}, func() float64 { return float64(s.PoolStats().Idle) })

	r.NewFuncCounter(metrics.Desc{
		Name: "edg_core_store_connection_waits_total",
		Help: "Times a caller had to wait for a free connection.",
	}, func() float64 { return float64(s.PoolStats().WaitCount) })

	// The real saturation signal for a single serialized SQLite handle: not
	// how many queries ran, but how long callers spent queued behind them.
	r.NewFuncCounter(metrics.Desc{
		Name: "edg_core_store_connection_wait_seconds_total",
		Help: "Total time callers spent waiting for a connection. Its rate is the saturation of the single SQLite handle.",
	}, func() float64 { return s.PoolStats().WaitDuration.Seconds() })

	// The counts are behind a cache: SELECT COUNT(*) shares the handle with
	// the ingest path, and /metrics is unauthenticated on the monitoring port,
	// so an uncached collector would let any caller force two table scans per
	// request.
	counts := &cachedStoreCounts{
		store:   s,
		ttl:     opts.TTL,
		timeout: opts.Timeout,
	}
	if counts.ttl <= 0 {
		counts.ttl = DefaultStoreCountTTL
	}
	if counts.timeout <= 0 {
		counts.timeout = DefaultStoreCountTimeout
	}

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_store_assets",
		Help: "Declared assets in master data, refreshed at most once per minute.",
	}, func() float64 { return float64(counts.get().assets) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_store_relations",
		Help: "Declared asset relations in master data, refreshed at most once per minute.",
	}, func() float64 { return float64(counts.get().relations) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_store_points",
		Help: "Declared points across every asset. This is the plant's tag inventory; compare it with edg_core_data_values_total to see how much of what is declared is actually reporting.",
	}, func() float64 { return float64(counts.get().points) })
}

type storeCounts struct {
	assets    int
	relations int
	points    int
}

// cachedStoreCounts serves the last successful counts and refreshes them at
// most once per TTL. On error or timeout it keeps serving the previous values:
// a query that failed says nothing about how many assets exist.
type cachedStoreCounts struct {
	store   *Store
	ttl     time.Duration
	timeout time.Duration

	mu      sync.Mutex
	value   storeCounts
	fetched time.Time
	// refreshing keeps a slow query from being started once per scrape.
	refreshing bool
}

func (c *cachedStoreCounts) get() storeCounts {
	c.mu.Lock()
	fresh := !c.fetched.IsZero() && time.Since(c.fetched) < c.ttl
	if fresh || c.refreshing {
		v := c.value
		c.mu.Unlock()
		return v
	}
	c.refreshing = true
	c.mu.Unlock()

	value, err := c.fetch()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	if err != nil {
		storeQueryFailures.Inc()
		log.Printf("[Core] metrics: asset counts unavailable, serving the previous values: %v", err)
		return c.value
	}
	c.value = value
	c.fetched = time.Now()
	return c.value
}

func (c *cachedStoreCounts) fetch() (storeCounts, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	var out storeCounts
	if err := c.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets`).Scan(&out.assets); err != nil {
		return storeCounts{}, err
	}
	if err := c.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_relations`).Scan(&out.relations); err != nil {
		return storeCounts{}, err
	}
	if err := c.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_points`).Scan(&out.points); err != nil {
		return storeCounts{}, err
	}
	return out, nil
}
