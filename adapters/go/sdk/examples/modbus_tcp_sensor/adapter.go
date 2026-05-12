package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/goburrow/modbus"

	"github.com/e7217/edg/adapters/go/sdk"
)

// ModbusDevice implements sdk.Collector + sdk.DeviceLifecycle for a single
// Modbus TCP slave. One ModbusDevice serves all registers from one host.
type ModbusDevice struct {
	cfg *ModbusConfig

	mu      sync.Mutex
	handler *modbus.TCPClientHandler
	client  modbus.Client
}

// NewModbusDevice builds a device but does not connect; the SDK calls
// ConnectDevice when the adapter starts.
func NewModbusDevice(cfg *ModbusConfig) *ModbusDevice {
	return &ModbusDevice{cfg: cfg}
}

func (d *ModbusDevice) ConnectDevice(_ context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	h := modbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", d.cfg.Host, d.cfg.Port))
	h.Timeout = time.Duration(d.cfg.Timeout * float64(time.Second))
	h.SlaveId = d.cfg.UnitID
	if err := h.Connect(); err != nil {
		return fmt.Errorf("%w: %v", sdk.ErrDeviceConnection, err)
	}
	d.handler = h
	d.client = modbus.NewClient(h)
	return nil
}

func (d *ModbusDevice) DisconnectDevice(_ context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handler != nil {
		_ = d.handler.Close()
		d.handler = nil
		d.client = nil
	}
	return nil
}

func (d *ModbusDevice) CheckDeviceHealth(ctx context.Context) error {
	// Cheap liveness check: read the first configured register.
	d.mu.Lock()
	client := d.client
	d.mu.Unlock()
	if client == nil {
		return fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}
	if len(d.cfg.Registers) == 0 {
		return nil
	}
	_, err := d.readWords(d.cfg.Registers[0])
	return err
}

func (d *ModbusDevice) Collect(_ context.Context) ([]sdk.TagValue, error) {
	d.mu.Lock()
	client := d.client
	d.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}

	values := make([]sdk.TagValue, 0, len(d.cfg.Registers))
	for _, spec := range d.cfg.Registers {
		words, err := d.readWords(spec)
		if err != nil {
			return nil, err
		}
		tv, err := DecodeRegister(words, spec)
		if err != nil {
			return nil, err
		}
		values = append(values, tv)
	}
	return values, nil
}

func (d *ModbusDevice) readWords(spec RegisterSpec) ([]uint16, error) {
	count, err := spec.WordCount()
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	client := d.client
	d.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}

	var raw []byte
	switch spec.Function {
	case "holding":
		raw, err = client.ReadHoldingRegisters(spec.Address, count)
	case "input":
		raw, err = client.ReadInputRegisters(spec.Address, count)
	default:
		return nil, fmt.Errorf("unsupported function: %s", spec.Function)
	}
	if err != nil {
		// goburrow surfaces Modbus exception responses and transport
		// errors both via the returned error. We don't try to
		// distinguish here — both feed the SDK's reconnect loop.
		return nil, fmt.Errorf("%w: read %s @%d: %v",
			sdk.ErrDeviceConnection, spec.Function, spec.Address, err)
	}

	words, err := bytesToWordsErr(raw)
	if err != nil {
		return nil, err
	}
	return words, nil
}

// bytesToWords converts goburrow's raw register payload (2 bytes per
// register, big-endian) into a slice of uint16 register values.
func bytesToWords(b []byte) []uint16 {
	w, _ := bytesToWordsErr(b)
	return w
}

func bytesToWordsErr(b []byte) ([]uint16, error) {
	if len(b)%2 != 0 {
		return nil, errors.New("packet length is not a multiple of 2")
	}
	out := make([]uint16, len(b)/2)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return out, nil
}
