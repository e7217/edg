package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// Point provisioning (ADR 0011). An adapter can take the list of readings it
// polls from master data instead of a local mapping file: core holds each
// asset's point list, the adapter fetches it at boot, and re-fetches when
// platform.meta.points.changed names its asset.

// Point is one declared reading. Mirrors internal/core/points.go.
type Point struct {
	Name      string `json:"name"`
	ValueType string `json:"value_type"`
	Unit      string `json:"unit,omitempty"`
	// Address and Encoding are opaque to core. Only the adapter that speaks
	// the protocol interprets them; Encoding keeps JSON types, so a numeric
	// scale arrives as a float64.
	Address  string         `json:"address"`
	Encoding map[string]any `json:"encoding,omitempty"`
	Enabled  bool           `json:"enabled"`
}

// PointList is every point declared for one asset.
type PointList struct {
	AssetID        string  `json:"asset_id"`
	Protocol       string  `json:"protocol"`
	PollIntervalMS int     `json:"poll_interval_ms,omitempty"`
	Version        int     `json:"version"`
	Points         []Point `json:"points"`
}

// EnabledPoints returns the points to poll. A disabled point is kept in
// master data for the record and must not be read.
func (pl *PointList) EnabledPoints() []Point {
	out := make([]Point, 0, len(pl.Points))
	for _, p := range pl.Points {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// GetPointList fetches an asset's point list. A declared asset with nothing
// declared yields an empty list at version 0; an unknown asset is ErrNotFound.
func (c *Client) GetPointList(ctx context.Context, assetID string) (*PointList, error) {
	var pl PointList
	if err := c.requestJSON(ctx, SubjectPointsGet, map[string]string{"asset_id": assetID}, &pl); err != nil {
		return nil, err
	}
	return &pl, nil
}

// CollectorFactory builds a collector from a point list. It is called once per
// list version, before the Adapter exists, so a collector never has to apply a
// new list while it is running. An error means the list cannot be used; the
// adapter then collects nothing and waits for the next version.
type CollectorFactory func(pl *PointList) (Collector, error)

// ProvisionedConfig configures RunProvisioned.
type ProvisionedConfig struct {
	// Adapter is the configuration of every Adapter built. Adapter.AssetID
	// names the point list; Adapter.CollectInterval is replaced by the list's
	// poll_interval_ms when that is set. ConfigVersion is set per list.
	Adapter AdapterConfig

	// RetryInterval between failed connects and fetches. Zero uses 5s. An
	// adapter that starts before core waits for it: NATS is embedded in core,
	// so there is nowhere to publish until core is up anyway.
	RetryInterval time.Duration

	// ReconcileInterval is how often the adapter re-reads its list even
	// without a change event. Events are best-effort NATS publishes and a
	// reconnect can drop one; this bounds how long a missed one leaves the
	// adapter on an old list. Zero uses 5m; negative disables it.
	ReconcileInterval time.Duration
}

// RunProvisioned runs an adapter whose points come from master data. It
// returns nil when ctx is cancelled, or the error of an Adapter that stopped
// for a reason other than a list change.
//
// Each list version gets a fresh Collector and a fresh Adapter: the previous
// generation is stopped, its device disconnected, and the next one built from
// the new list. Resolving the list before NewAdapter, rather than handing a
// running collector a new list, is what makes a change safe without locks in
// Collect.
func RunProvisioned(ctx context.Context, cfg ProvisionedConfig, factory CollectorFactory) error {
	if cfg.Adapter.AssetID == "" {
		return errors.New("sdk: ProvisionedConfig.Adapter.AssetID is required")
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = 5 * time.Second
	}
	if cfg.ReconcileInterval == 0 {
		cfg.ReconcileInterval = 5 * time.Minute
	}
	cfg.Adapter.applyDefaults()
	log := cfg.Adapter.Logger.With("asset_id", cfg.Adapter.AssetID)

	// One connection for every generation: an Adapter closes a client only if
	// it built it, so passing ours keeps the subscription below alive across
	// rebuilds.
	client := cfg.Adapter.Client
	if client == nil {
		client = NewClient(Options{URL: cfg.Adapter.NATSURL, Name: cfg.Adapter.AssetID, Logger: cfg.Adapter.Logger})
		defer client.Close()
	}
	if err := connectWithRetry(ctx, client, cfg.RetryInterval, log); err != nil {
		return nil
	}
	cfg.Adapter.Client = client

	// Subscribed before the first fetch, so a change that lands between the
	// fetch and the subscription cannot be missed.
	changed := make(chan int, 1)
	unsubscribe, err := subscribePointsChanged(client, cfg.Adapter.AssetID, changed)
	if err != nil {
		return fmt.Errorf("sdk: subscribe %s: %w", SubjectPointsChanged, err)
	}
	defer unsubscribe()

	pl, err := fetchWithRetry(ctx, client, cfg, log)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	applied := 0 // the last version a collector could be built from
	for {
		next, runErr, err := runUntilChange(ctx, client, cfg, pl, &applied, changed, factory, log)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if next == nil {
			return runErr
		}
		if next.Version != pl.Version {
			log.Info("point list changed; rebuilding", "from_version", pl.Version, "to_version", next.Version)
		}
		pl = next
	}
}

// runUntilChange runs one generation. It returns the list to rebuild from, or
// nil and the Adapter's own error if the generation ended by itself.
func runUntilChange(ctx context.Context, client *Client, cfg ProvisionedConfig, pl *PointList,
	applied *int, changed <-chan int, factory CollectorFactory, log *slog.Logger,
) (*PointList, error, error) {
	genCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a := buildGeneration(cfg, pl, applied, factory, log)
	done := make(chan error, 1)
	go func() { done <- a.Run(genCtx) }()

	var reconcile <-chan time.Time
	if cfg.ReconcileInterval > 0 {
		t := time.NewTicker(cfg.ReconcileInterval)
		defer t.Stop()
		reconcile = t.C
	}

	stop := func() { cancel(); <-done }
	for {
		select {
		case err := <-done:
			return nil, err, nil
		case v := <-changed:
			if v != 0 && v <= pl.Version {
				continue // an older or duplicate event
			}
			stop()
			next, err := fetchWithRetry(ctx, client, cfg, log)
			return next, nil, err
		case <-reconcile:
			current, err := client.GetPointList(ctx, cfg.Adapter.AssetID)
			if err != nil {
				continue // still unreachable, or a blip; try again next tick
			}
			if current.Version == pl.Version {
				continue
			}
			stop()
			return current, nil, nil
		}
	}
}

// buildGeneration builds the Adapter for pl.
//
// An unusable list is reported and waited out rather than returned: the fix is
// a new list, which arrives as a change event. Meanwhile the adapter keeps
// heartbeating and reports the last version it could actually run, so the
// drift report shows it as stale on the declared one instead of converged on a
// list it cannot poll. Its collector fails every tick with the reason, which
// is what puts the reason in the status frame's last_error.
func buildGeneration(cfg ProvisionedConfig, pl *PointList, applied *int, factory CollectorFactory, log *slog.Logger) *Adapter {
	acfg := cfg.Adapter
	if pl.PollIntervalMS > 0 {
		acfg.CollectInterval = time.Duration(pl.PollIntervalMS) * time.Millisecond
	}
	collector, err := factory(pl)
	if err != nil {
		log.Error("point list is unusable; collecting nothing until it changes",
			"version", pl.Version, "err", err)
		acfg.ConfigVersion = *applied
		// Slow ticks: each one only re-reports the same error.
		acfg.CollectInterval = unusableListInterval
		return NewAdapter(acfg, unusableList{fmt.Errorf("point list v%d is unusable: %w", pl.Version, err)})
	}
	*applied = pl.Version
	acfg.ConfigVersion = pl.Version
	return NewAdapter(acfg, collector)
}

const unusableListInterval = 10 * time.Second

// unusableList stands in for a collector that could not be built.
type unusableList struct{ err error }

func (u unusableList) Collect(context.Context) ([]TagValue, error) { return nil, u.err }

func fetchWithRetry(ctx context.Context, client *Client, cfg ProvisionedConfig, log *slog.Logger) (*PointList, error) {
	for {
		pl, err := client.GetPointList(ctx, cfg.Adapter.AssetID)
		if err == nil {
			return pl, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("sdk: asset %q is not declared in master data: %w", cfg.Adapter.AssetID, err)
		}
		log.Warn("fetching point list failed; retrying", "err", err, "retry_in", cfg.RetryInterval)
		if !sleepCtx(ctx, cfg.RetryInterval) {
			return nil, ctx.Err()
		}
	}
}

func connectWithRetry(ctx context.Context, client *Client, retry time.Duration, log *slog.Logger) error {
	for {
		err := client.Connect(ctx)
		if err == nil {
			return nil
		}
		log.Warn("connecting to core failed; retrying", "err", err, "retry_in", retry)
		if !sleepCtx(ctx, retry) {
			return ctx.Err()
		}
	}
}

// pointsChangedEvent is the part of MetaChangeEvent this needs.
type pointsChangedEvent struct {
	EntityID string `json:"entity_id"`
	After    *struct {
		Version int `json:"version"`
	} `json:"after,omitempty"`
}

// subscribePointsChanged forwards the new version for assetID to out. A
// delete carries no after and is forwarded as version 0: the list is gone,
// which is a change like any other.
func subscribePointsChanged(client *Client, assetID string, out chan int) (func(), error) {
	nc, err := client.conn()
	if err != nil {
		return nil, err
	}
	sub, err := nc.Subscribe(SubjectPointsChanged, func(msg *nats.Msg) {
		var ev pointsChangedEvent
		if json.Unmarshal(msg.Data, &ev) != nil || ev.EntityID != assetID {
			return
		}
		v := 0
		if ev.After != nil {
			v = ev.After.Version
		}
		// Latest wins: replace a value the loop has not read yet.
		for {
			select {
			case out <- v:
				return
			default:
			}
			select {
			case <-out:
			default:
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return func() { _ = sub.Unsubscribe() }, nil
}
