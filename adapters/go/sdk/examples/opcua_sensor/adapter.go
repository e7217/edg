package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"

	"github.com/e7217/edg/adapters/go/sdk"
)

// Device reads a fixed set of variables from one OPC UA server. It implements
// sdk.Collector and sdk.DeviceLifecycle.
type Device struct {
	cfg   *Config
	nodes []Node
	log   *slog.Logger

	mu     sync.Mutex
	client *opcua.Client
}

// NewDevice builds a device but does not connect; the SDK calls ConnectDevice.
func NewDevice(cfg *Config, nodes []Node) *Device {
	return &Device{cfg: cfg, nodes: nodes, log: slog.Default()}
}

func (d *Device) ConnectDevice(ctx context.Context) error {
	opts := []opcua.Option{
		opcua.SecurityMode(ua.MessageSecurityModeNone),
		opcua.RequestTimeout(d.cfg.timeout()),
		opcua.AutoReconnect(false), // the SDK owns reconnection and its backoff
	}
	if d.cfg.Username != "" {
		opts = append(opts, opcua.AuthUsername(d.cfg.Username, d.cfg.Password), opcua.SecurityPolicy("None"))
	} else {
		opts = append(opts, opcua.AuthAnonymous())
	}
	c, err := opcua.NewClient(d.cfg.Endpoint, opts...)
	if err != nil {
		return fmt.Errorf("%w: %v", sdk.ErrDeviceConnection, err)
	}
	cctx, cancel := context.WithTimeout(ctx, d.cfg.timeout())
	defer cancel()
	if err := c.Connect(cctx); err != nil {
		return fmt.Errorf("%w: connect %s: %v", sdk.ErrDeviceConnection, d.cfg.Endpoint, err)
	}
	d.mu.Lock()
	d.client = c
	d.mu.Unlock()
	return nil
}

func (d *Device) DisconnectDevice(ctx context.Context) error {
	d.mu.Lock()
	c := d.client
	d.client = nil
	d.mu.Unlock()
	if c != nil {
		return c.Close(ctx)
	}
	return nil
}

func (d *Device) CheckDeviceHealth(context.Context) error {
	d.mu.Lock()
	c := d.client
	d.mu.Unlock()
	if c == nil || c.State() != opcua.Connected {
		return fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}
	return nil
}

// Collect reads every node in one Read request.
//
// A node whose status is Bad yields no value -- there is no reading to report,
// and the data contract would reject an empty one -- and is logged; Uncertain
// is reported with that quality. A failed request is a device error, so the
// SDK reconnects.
func (d *Device) Collect(ctx context.Context) ([]sdk.TagValue, error) {
	d.mu.Lock()
	c := d.client
	d.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}
	if len(d.nodes) == 0 {
		return nil, nil
	}
	req := &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnNeither}
	for _, n := range d.nodes {
		req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: n.id, AttributeID: ua.AttributeIDValue})
	}
	rctx, cancel := context.WithTimeout(ctx, d.cfg.timeout())
	defer cancel()
	resp, err := c.Read(rctx, req)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) || isChannelError(err) {
			return nil, fmt.Errorf("%w: read: %v", sdk.ErrDeviceConnection, err)
		}
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(resp.Results) != len(d.nodes) {
		return nil, fmt.Errorf("read returned %d results for %d nodes", len(resp.Results), len(d.nodes))
	}

	out := make([]sdk.TagValue, 0, len(d.nodes))
	for i, r := range resp.Results {
		n := d.nodes[i]
		quality, ok := qualityOf(r.Status)
		if !ok {
			d.log.Warn("opc ua node read failed", "name", n.Name, "node_id", n.NodeID, "status", r.Status)
			continue
		}
		tv, ok := tagValue(n, r.Value)
		if !ok {
			d.log.Warn("opc ua value type not mapped", "name", n.Name, "node_id", n.NodeID, "type", typeName(r.Value))
			continue
		}
		tv.Quality = quality
		out = append(out, tv)
	}
	return out, nil
}

func isChannelError(err error) bool {
	return errors.Is(err, ua.StatusBadSessionIDInvalid) || errors.Is(err, ua.StatusBadSessionNotActivated) ||
		errors.Is(err, ua.StatusBadSecureChannelIDInvalid) || errors.Is(err, ua.StatusBadConnectionClosed) ||
		errors.Is(err, ua.StatusBadServerNotConnected) || errors.Is(err, ua.StatusBadTimeout)
}

// qualityOf maps an OPC UA status to a quality, and false for Bad: the top two
// bits of a StatusCode are its severity.
func qualityOf(s ua.StatusCode) (string, bool) {
	switch uint32(s) >> 30 {
	case 0:
		return sdk.QualityGood, true
	case 1:
		return sdk.QualityUncertain, true
	default:
		return "", false
	}
}

// tagValue maps a Variant onto the three reading kinds.
func tagValue(n Node, v *ua.Variant) (sdk.TagValue, bool) {
	tv := sdk.TagValue{Name: n.Name, Unit: n.Unit}
	if v == nil {
		return tv, false
	}
	var f float64
	switch x := v.Value().(type) {
	case bool:
		tv.Flag = &x
		tv.Unit = ""
		return tv, true
	case string:
		tv.Text = &x
		tv.Unit = ""
		return tv, true
	case *ua.LocalizedText:
		s := x.Text
		tv.Text = &s
		tv.Unit = ""
		return tv, true
	case int8:
		f = float64(x)
	case uint8:
		f = float64(x)
	case int16:
		f = float64(x)
	case uint16:
		f = float64(x)
	case int32:
		f = float64(x)
	case uint32:
		f = float64(x)
	case int64:
		f = float64(x)
	case uint64:
		f = float64(x)
	case float32:
		f = float64(x)
	case float64:
		f = x
	case time.Time:
		f = float64(x.UnixMilli())
	default:
		return tv, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return tv, false
	}
	tv.Number = &f
	return tv, true
}

func typeName(v *ua.Variant) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%T", v.Value())
}
