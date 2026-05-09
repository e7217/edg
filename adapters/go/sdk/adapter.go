package sdk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Collector produces tag values on demand. Implementations must be safe to
// call repeatedly from a single goroutine.
type Collector interface {
	Collect(ctx context.Context) ([]TagValue, error)
}

// DeviceLifecycle is implemented by collectors that need to manage a
// connection to an external device. The Adapter calls Connect/Disconnect
// around the collect loop and CheckHealth before each tick.
type DeviceLifecycle interface {
	ConnectDevice(ctx context.Context) error
	DisconnectDevice(ctx context.Context) error
	CheckDeviceHealth(ctx context.Context) error
}

// DeviceObserver is invoked on device state transitions.
type DeviceObserver interface {
	OnDeviceConnected(ctx context.Context)
	OnDeviceDisconnected(ctx context.Context, err error)
	OnDeviceReconnected(ctx context.Context)
}

// AdapterConfig configures an Adapter.
type AdapterConfig struct {
	// AssetID is the asset identifier published with each AssetData. Required.
	AssetID string

	// NATSURL is the NATS server URL. Ignored when Client is supplied.
	NATSURL string

	// CollectInterval between Collector.Collect calls. Default 1s.
	CollectInterval time.Duration

	// Metadata, if non-nil, is attached to every published AssetData.
	Metadata map[string]string

	// Client, when non-nil, is used as-is. Otherwise a Client is built from
	// NATSURL.
	Client *Client

	// Backoff for device reconnection. Zero value uses DefaultBackoff().
	Backoff Backoff

	// MaxRetries is the maximum number of consecutive device connect
	// attempts. Zero uses 5; a negative value means unlimited.
	MaxRetries int

	// Logger is used for adapter-level logging. Zero value uses slog.Default().
	Logger *slog.Logger
}

func (c *AdapterConfig) applyDefaults() {
	if c.CollectInterval == 0 {
		c.CollectInterval = 1 * time.Second
	}
	if c.Backoff == (Backoff{}) {
		c.Backoff = DefaultBackoff()
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 5
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Adapter wires a Collector to EDG Core. Build with NewAdapter and start
// with Run; the goroutine running Run owns the adapter's lifecycle.
type Adapter struct {
	cfg       AdapterConfig
	collector Collector
	lifecycle DeviceLifecycle // nil if collector does not implement it
	observer  DeviceObserver  // nil if collector does not implement it

	ownsClient bool
	client     *Client

	mu    sync.RWMutex
	state DeviceState
}

// NewAdapter returns an Adapter for the given Collector. If c also
// implements DeviceLifecycle or DeviceObserver, those hooks are wired up.
func NewAdapter(cfg AdapterConfig, c Collector) *Adapter {
	cfg.applyDefaults()

	a := &Adapter{
		cfg:       cfg,
		collector: c,
		state:     DeviceDisconnected,
	}
	if lc, ok := c.(DeviceLifecycle); ok {
		a.lifecycle = lc
	}
	if obs, ok := c.(DeviceObserver); ok {
		a.observer = obs
	}
	if cfg.Client != nil {
		a.client = cfg.Client
	} else {
		a.client = NewClient(Options{URL: cfg.NATSURL, Name: cfg.AssetID, Logger: cfg.Logger})
		a.ownsClient = true
	}
	return a
}

// DeviceState returns the current device state. Safe for concurrent use.
func (a *Adapter) DeviceState() DeviceState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *Adapter) setState(s DeviceState) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
}

// Run connects to NATS, then runs the collect loop until ctx is cancelled.
// On exit it disconnects from the device (if a lifecycle is registered) and,
// if the adapter owns the Client, closes the NATS connection.
//
// Run is safe to call exactly once per Adapter.
func (a *Adapter) Run(ctx context.Context) error {
	if a.cfg.AssetID == "" {
		return errors.New("sdk: AdapterConfig.AssetID is required")
	}

	a.cfg.Logger.Info("adapter starting",
		"asset_id", a.cfg.AssetID,
		"interval", a.cfg.CollectInterval,
	)

	if err := a.client.Connect(ctx); err != nil {
		return err
	}
	defer func() {
		if a.ownsClient {
			if err := a.client.Close(); err != nil {
				a.cfg.Logger.Warn("client close", "err", err)
			}
		}
	}()

	connected, err := a.ensureDeviceConnected(ctx, false)
	if err != nil {
		return err
	}
	defer a.disconnectDevice(context.Background(), connected)

	ticker := time.NewTicker(a.cfg.CollectInterval)
	defer ticker.Stop()

	// First collect happens after the first tick, mirroring the Python SDK
	// (sleep(interval) at the end of the loop).
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if err := a.tick(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			// Device errors are already handled inside tick; other errors
			// are logged and the loop continues.
			a.cfg.Logger.Error("adapter tick", "err", err)
		}
	}
}

// tick runs one iteration of the collect loop.
func (a *Adapter) tick(ctx context.Context) error {
	if _, err := a.ensureDeviceConnected(ctx, true); err != nil {
		return err
	}
	if a.lifecycle != nil {
		if err := a.lifecycle.CheckDeviceHealth(ctx); err != nil {
			a.handleDeviceError(ctx, err)
			return err
		}
	}
	values, err := a.collector.Collect(ctx)
	if err != nil {
		if isDeviceError(err) {
			a.handleDeviceError(ctx, err)
		}
		return err
	}
	if len(values) == 0 {
		return nil
	}
	data := AssetData{
		AssetID:  a.cfg.AssetID,
		Values:   values,
		Metadata: a.cfg.Metadata,
	}
	if err := a.client.PublishAssetData(ctx, data); err != nil {
		return err
	}
	a.cfg.Logger.Debug("published", "asset_id", a.cfg.AssetID, "tags", len(values))
	return nil
}

// ensureDeviceConnected drives the connect/reconnect state machine. The
// reconnecting flag distinguishes a fresh start from an in-loop retry.
// Returns true if a connect actually happened (so callers can defer
// disconnect).
func (a *Adapter) ensureDeviceConnected(ctx context.Context, reconnecting bool) (bool, error) {
	if a.lifecycle == nil {
		a.setState(DeviceConnected)
		return false, nil
	}
	if a.DeviceState() == DeviceConnected {
		return true, nil
	}

	wasReconnecting := reconnecting
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if wasReconnecting {
			a.setState(DeviceReconnecting)
		} else {
			a.setState(DeviceConnecting)
		}

		err := a.lifecycle.ConnectDevice(ctx)
		if err == nil {
			a.setState(DeviceConnected)
			if a.observer != nil {
				if wasReconnecting {
					a.observer.OnDeviceReconnected(ctx)
				} else {
					a.observer.OnDeviceConnected(ctx)
				}
			}
			return true, nil
		}

		if !isDeviceError(err) {
			a.setState(DeviceError)
			return false, err
		}

		attempt++
		if a.cfg.MaxRetries >= 0 && attempt >= a.cfg.MaxRetries {
			a.setState(DeviceError)
			if a.observer != nil {
				a.observer.OnDeviceDisconnected(ctx, err)
			}
			return false, fmt.Errorf("%w: failed to connect after %d attempts: %w", ErrDevice, a.cfg.MaxRetries, err)
		}

		delay := a.cfg.Backoff.NextDelay(attempt - 1)
		a.cfg.Logger.Warn("device connect attempt failed",
			"attempt", attempt,
			"err", err,
			"retry_in", delay,
		)
		wasReconnecting = true
		if !sleepCtx(ctx, delay) {
			return false, ctx.Err()
		}
	}
}

// handleDeviceError marks the device as disconnected and notifies observers.
// The collect loop will reconnect on the next tick.
func (a *Adapter) handleDeviceError(ctx context.Context, err error) {
	if a.lifecycle == nil {
		return
	}
	a.setState(DeviceDisconnected)
	if a.observer != nil {
		a.observer.OnDeviceDisconnected(ctx, err)
	}
}

func (a *Adapter) disconnectDevice(ctx context.Context, connected bool) {
	if !connected || a.lifecycle == nil {
		return
	}
	if err := a.lifecycle.DisconnectDevice(ctx); err != nil {
		a.cfg.Logger.Warn("device disconnect", "err", err)
	}
	a.setState(DeviceDisconnected)
}

func isDeviceError(err error) bool {
	return errors.Is(err, ErrDevice)
}

// sleepCtx returns true if it slept the full duration, false if ctx ended.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
