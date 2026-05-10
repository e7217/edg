package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Options configures Client. Zero values are replaced by sensible defaults.
type Options struct {
	URL                  string        // default "nats://localhost:4222"
	Name                 string        // optional, used as the NATS connection name
	MaxReconnectAttempts int           // default -1 (unlimited); 0 disables reconnect
	ReconnectWait        time.Duration // default 2s
	ConnectTimeout       time.Duration // default 2s
	RequestTimeout       time.Duration // default 5s
	Logger               *slog.Logger  // default slog.Default()
}

func (o *Options) applyDefaults() {
	if o.URL == "" {
		o.URL = nats.DefaultURL
	}
	if o.MaxReconnectAttempts == 0 {
		o.MaxReconnectAttempts = -1
	}
	if o.ReconnectWait == 0 {
		o.ReconnectWait = 2 * time.Second
	}
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = 2 * time.Second
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 5 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Client is a thin wrapper around *nats.Conn that exposes the EDG Core
// subject contract.
type Client struct {
	opts Options

	mu sync.RWMutex
	nc *nats.Conn
}

// NewClient builds a Client. It does not open the connection; call Connect.
func NewClient(opts Options) *Client {
	opts.applyDefaults()
	return &Client{opts: opts}
}

// Wrap returns a Client that uses the given pre-existing connection. The
// caller retains ownership of nc (Close on the returned Client is a no-op).
func Wrap(nc *nats.Conn, opts Options) *Client {
	opts.applyDefaults()
	return &Client{opts: opts, nc: nc}
}

// Connect dials the NATS server. It is safe to call multiple times.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.nc != nil && c.nc.IsConnected() {
		return nil
	}

	natsOpts := []nats.Option{
		nats.MaxReconnects(c.opts.MaxReconnectAttempts),
		nats.ReconnectWait(c.opts.ReconnectWait),
		nats.Timeout(c.opts.ConnectTimeout),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			c.opts.Logger.Error("nats error", "err", err)
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				c.opts.Logger.Warn("nats disconnected", "err", err)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			c.opts.Logger.Info("nats reconnected", "url", nc.ConnectedUrl())
		}),
	}
	if c.opts.Name != "" {
		natsOpts = append(natsOpts, nats.Name(c.opts.Name))
	}

	// nats.Connect itself respects ConnectTimeout; ctx deadline is checked
	// before issuing the dial as a best-effort early bail-out.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrConnection, err)
	}

	nc, err := nats.Connect(c.opts.URL, natsOpts...)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConnection, err)
	}
	c.nc = nc
	c.opts.Logger.Info("nats connected", "url", c.opts.URL)
	return nil
}

// Close drains and closes the underlying connection. Safe to call repeatedly.
// If the client was created with Wrap, Close is a no-op.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nc == nil {
		return nil
	}
	if err := c.nc.Drain(); err != nil {
		return fmt.Errorf("%w: drain: %w", ErrConnection, err)
	}
	c.nc = nil
	return nil
}

// IsConnected reports whether the client is currently connected.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nc != nil && c.nc.IsConnected()
}

// conn returns the active nats.Conn or an error.
func (c *Client) conn() (*nats.Conn, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nc == nil {
		return nil, fmt.Errorf("%w: not connected", ErrPublish)
	}
	return c.nc, nil
}

// PublishAssetData publishes data to SubjectAssetData. If data.Timestamp is
// zero it is set to the current time in epoch milliseconds.
func (c *Client) PublishAssetData(ctx context.Context, data AssetData) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	nc, err := c.conn()
	if err != nil {
		return err
	}
	if data.Timestamp == 0 {
		data.Timestamp = time.Now().UnixMilli()
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("%w: marshal asset data: %w", ErrPublish, err)
	}
	if err := nc.Publish(SubjectAssetData, payload); err != nil {
		return fmt.Errorf("%w: %w", ErrPublish, err)
	}
	return nil
}

// CreateAsset issues a request to SubjectAssetCreate. Returns the created
// asset on success.
func (c *Client) CreateAsset(ctx context.Context, req CreateAssetRequest) (*Asset, error) {
	var asset Asset
	if err := c.requestJSON(ctx, SubjectAssetCreate, req, &asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// GetAsset issues a request to SubjectAssetGet. Returns (nil, nil) if Core
// replies with an "asset not found" error (Python SDK parity).
func (c *Client) GetAsset(ctx context.Context, req GetAssetRequest) (*Asset, error) {
	var asset Asset
	if err := c.requestJSON(ctx, SubjectAssetGet, req, &asset); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &asset, nil
}

// ListAssets issues a request to SubjectAssetList.
func (c *Client) ListAssets(ctx context.Context) ([]Asset, error) {
	var out []Asset
	if err := c.requestJSON(ctx, SubjectAssetList, struct{}{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateAsset issues a request to SubjectAssetUpdate.
func (c *Client) UpdateAsset(ctx context.Context, req UpdateAssetRequest) (*Asset, error) {
	var asset Asset
	if err := c.requestJSON(ctx, SubjectAssetUpdate, req, &asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// DeleteAsset issues a request to SubjectAssetDelete.
func (c *Client) DeleteAsset(ctx context.Context, req DeleteAssetRequest) error {
	return c.requestJSON(ctx, SubjectAssetDelete, req, nil)
}

// CreateRelation issues a request to SubjectRelationCreate.
func (c *Client) CreateRelation(ctx context.Context, req CreateRelationRequest) (*AssetRelation, error) {
	var rel AssetRelation
	if err := c.requestJSON(ctx, SubjectRelationCreate, req, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// GetRelation issues a request to SubjectRelationGet. Returns (nil, nil) if
// Core replies with a "relation not found" error.
func (c *Client) GetRelation(ctx context.Context, id string) (*AssetRelation, error) {
	var rel AssetRelation
	if err := c.requestJSON(ctx, SubjectRelationGet, map[string]string{"id": id}, &rel); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

// ListRelations issues a request to SubjectRelationList.
func (c *Client) ListRelations(ctx context.Context, req ListRelationsRequest) ([]AssetRelation, error) {
	var out []AssetRelation
	if err := c.requestJSON(ctx, SubjectRelationList, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteRelation issues a request to SubjectRelationDelete.
func (c *Client) DeleteRelation(ctx context.Context, id string) error {
	return c.requestJSON(ctx, SubjectRelationDelete, map[string]string{"id": id}, nil)
}

// MetaChangeHandler is invoked for each incoming change event. The handler
// runs on the NATS subscription goroutine; do not block it for long.
type MetaChangeHandler func(MetaChangeEvent)

// Subscription is an unsubscribable handle.
type Subscription interface {
	Unsubscribe() error
}

// SubscribeMetaChanges subscribes to SubjectMetaChangedAll and dispatches
// every received event to handler. The returned Subscription should be
// Unsubscribed when no longer needed.
func (c *Client) SubscribeMetaChanges(handler MetaChangeHandler) (Subscription, error) {
	if handler == nil {
		return nil, fmt.Errorf("%w: nil handler", ErrPublish)
	}
	nc, err := c.conn()
	if err != nil {
		return nil, err
	}
	sub, err := nc.Subscribe(SubjectMetaChangedAll, func(msg *nats.Msg) {
		var ev MetaChangeEvent
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			c.opts.Logger.Warn("decode meta change event", "err", err, "subject", msg.Subject)
			return
		}
		handler(ev)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: subscribe %s: %w", ErrPublish, SubjectMetaChangedAll, err)
	}
	return sub, nil
}

// requestJSON marshals req, issues a NATS request, and decodes the
// coreResponse. If out is non-nil and Data is present it is unmarshaled into
// out. ctx deadline overrides the default RequestTimeout.
func (c *Client) requestJSON(ctx context.Context, subject string, req any, out any) error {
	nc, err := c.conn()
	if err != nil {
		return err
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%w: marshal %s: %w", ErrPublish, subject, err)
	}

	reqCtx, cancel := requestContext(ctx, c.opts.RequestTimeout)
	defer cancel()
	msg, err := nc.RequestWithContext(reqCtx, subject, payload)
	if err != nil {
		return fmt.Errorf("%w: request %s: %w", ErrPublish, subject, err)
	}

	var resp coreResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("%w: decode response from %s: %w", ErrPublish, subject, err)
	}
	if !resp.Success {
		if isNotFound(resp.Error) {
			return ErrNotFound
		}
		return &CoreError{Subject: subject, Message: resp.Error}
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("%w: decode response data from %s: %w", ErrPublish, subject, err)
	}
	return nil
}

func isNotFound(msg string) bool {
	// Core replies use phrases like "asset not found" / "relation not found".
	return strings.Contains(strings.ToLower(msg), "not found")
}

// requestContext returns ctx (and a no-op cancel) if it already has a
// deadline tighter than d, otherwise a context.WithTimeout-derived one.
func requestContext(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= d {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}
