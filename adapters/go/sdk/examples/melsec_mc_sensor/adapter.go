package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/e7217/edg/adapters/go/sdk"
)

// PLC reads a set of devices from one MELSEC CPU over the MC protocol. It
// implements sdk.Collector and sdk.DeviceLifecycle.
type PLC struct {
	cfg   *Config
	specs []PointSpec

	mu     sync.Mutex
	client *MCClient
}

func NewPLC(cfg *Config, specs []PointSpec) *PLC { return &PLC{cfg: cfg, specs: specs} }

func (p *PLC) ConnectDevice(context.Context) error {
	c, err := DialMC(net.JoinHostPort(p.cfg.Host, strconv.Itoa(p.cfg.Port)), p.cfg.timeout())
	if err != nil {
		return fmt.Errorf("%w: %v", sdk.ErrDeviceConnection, err)
	}
	p.mu.Lock()
	p.client = c
	p.mu.Unlock()
	return nil
}

func (p *PLC) DisconnectDevice(context.Context) error {
	p.mu.Lock()
	c := p.client
	p.client = nil
	p.mu.Unlock()
	if c != nil {
		return c.Close()
	}
	return nil
}

func (p *PLC) CheckDeviceHealth(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}
	return nil
}

// Collect reads each point. A PLC end code (the device does not exist, is out
// of range, is protected) is reported for that point and the rest are read; a
// transport error is a device error, so the SDK reconnects.
func (p *PLC) Collect(context.Context) ([]sdk.TagValue, error) {
	p.mu.Lock()
	c := p.client
	p.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}
	out := make([]sdk.TagValue, 0, len(p.specs))
	var refused []string
	for _, s := range p.specs {
		words, err := c.ReadWords(s.Device, s.Words())
		var end ErrEndCode
		if errors.As(err, &end) {
			refused = append(refused, fmt.Sprintf("%s (%s): %v", s.Name, s.Device.Name, end))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", sdk.ErrDeviceConnection, s.Device.Name, err)
		}
		tv, err := decode(s, words)
		if err != nil {
			return nil, err
		}
		out = append(out, tv)
	}
	if len(refused) > 0 && len(out) == 0 {
		return nil, fmt.Errorf("PLC refused every read: %v", refused)
	}
	if len(refused) > 0 {
		// Reported through the collect-error path would drop the whole
		// poll; a refused device is a configuration problem for that point.
		slog.Warn("PLC refused some reads; the rest were published", "refused", refused)
	}
	return out, nil
}
