// Command temp_sensor_with_recovery shows how to plug device connect /
// disconnect / health hooks into an Adapter. The "device" is simulated and
// flips between healthy and faulted states to exercise the reconnect path.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/e7217/edg/adapters/go/sdk"
)

type flakyDevice struct {
	connected atomic.Bool
	ticks     atomic.Int64
}

func (d *flakyDevice) ConnectDevice(_ context.Context) error {
	// 30% of the time the connection attempt fails — Adapter retries with
	// exponential backoff.
	if rand.Float64() < 0.3 {
		return fmt.Errorf("%w: simulated transport error", sdk.ErrDeviceConnection)
	}
	d.connected.Store(true)
	return nil
}

func (d *flakyDevice) DisconnectDevice(_ context.Context) error {
	d.connected.Store(false)
	return nil
}

func (d *flakyDevice) CheckDeviceHealth(_ context.Context) error {
	// Once every ~20 ticks the device "drops".
	if d.ticks.Add(1)%20 == 0 {
		d.connected.Store(false)
		return fmt.Errorf("%w: device dropped", sdk.ErrDeviceConnection)
	}
	return nil
}

func (d *flakyDevice) Collect(_ context.Context) ([]sdk.TagValue, error) {
	if !d.connected.Load() {
		return nil, fmt.Errorf("%w: not connected", sdk.ErrDeviceConnection)
	}
	temp := 20 + rand.Float64()*10
	return []sdk.TagValue{{Name: "temperature", Quality: sdk.QualityGood, Number: &temp, Unit: "°C"}}, nil
}

func (d *flakyDevice) OnDeviceConnected(_ context.Context) {
	log.Println("device connected")
}

func (d *flakyDevice) OnDeviceDisconnected(_ context.Context, err error) {
	if errors.Is(err, sdk.ErrDeviceConnection) {
		log.Printf("device disconnected: %v", err)
	}
}

func (d *flakyDevice) OnDeviceReconnected(_ context.Context) {
	log.Println("device reconnected")
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := sdk.NewAdapter(sdk.AdapterConfig{
		AssetID:         "temp-sensor-002",
		CollectInterval: 500 * time.Millisecond,
		MaxRetries:      -1, // retry forever; suitable for long-running adapters
		Metadata:        map[string]string{"location": "factory-b", "protocol": "virtual-flaky"},
	}, &flakyDevice{})

	if err := a.Run(ctx); err != nil {
		log.Fatalf("adapter exited: %v", err)
	}
}
