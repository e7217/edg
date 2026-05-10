# EDG Platform Go SDK

Go SDK for building EDG Platform adapters. Provides typed wrappers around the
NATS subject contract documented in [`docs/ADAPTER_GUIDE.md`](../../../docs/ADAPTER_GUIDE.md)
and [`docs/events.md`](../../../docs/events.md), so adapter authors do not need
to hand-roll wire types, request/reply, or reconnection logic.

Feature parity with the [Python SDK](../../python/sdk):

- Publish `AssetData` to `platform.data.asset`
- Asset and relation CRUD via NATS request/reply
- Subscribe to `platform.meta.*.changed` events
- `Adapter` skeleton with collect loop, exponential backoff, device connect /
  reconnect / health hooks

## Install

```bash
go get github.com/e7217/edg/adapters/go/sdk
```

The SDK is its own Go module so you only pull `nats.go` (and its minimal
transitive dependencies) — the EDG Core's heavier deps (sqlite, migrate,
embedded `nats-server`) are not transitively required.

## Quick start

```go
package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"
    "time"

    "github.com/e7217/edg/adapters/go/sdk"
)

type tempSensor struct{}

func (tempSensor) Collect(_ context.Context) ([]sdk.TagValue, error) {
    n := 25.5
    return []sdk.TagValue{
        {Name: "temperature", Quality: sdk.QualityGood, Number: &n, Unit: "°C"},
    }, nil
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    a := sdk.NewAdapter(sdk.AdapterConfig{
        AssetID:         "temp-sensor-001",
        CollectInterval: time.Second,
    }, tempSensor{})

    if err := a.Run(ctx); err != nil {
        log.Fatalf("adapter exited: %v", err)
    }
}
```

## Optional device hooks

If your collector also implements `DeviceLifecycle` and `DeviceObserver`,
the adapter calls those hooks around the collect loop:

```go
type myDevice struct{ /* ... */ }

func (d *myDevice) ConnectDevice(ctx context.Context) error    { /* ... */ }
func (d *myDevice) DisconnectDevice(ctx context.Context) error { /* ... */ }
func (d *myDevice) CheckDeviceHealth(ctx context.Context) error { /* ... */ }
func (d *myDevice) Collect(ctx context.Context) ([]sdk.TagValue, error) { /* ... */ }

func (d *myDevice) OnDeviceConnected(ctx context.Context)              { /* ... */ }
func (d *myDevice) OnDeviceDisconnected(ctx context.Context, err error) { /* ... */ }
func (d *myDevice) OnDeviceReconnected(ctx context.Context)            { /* ... */ }
```

Connect failures are retried with exponential backoff configured via
`AdapterConfig.Backoff` and `AdapterConfig.MaxRetries`. Wrap retryable errors
with `sdk.ErrDeviceConnection` or `sdk.ErrDeviceTimeout` so the adapter
classifies them correctly. See
[`examples/temp_sensor_with_recovery`](examples/temp_sensor_with_recovery)
for a runnable example.

## Direct client use

If you do not need the adapter loop you can use the `Client` directly:

```go
c := sdk.NewClient(sdk.Options{URL: "nats://localhost:4222"})
if err := c.Connect(ctx); err != nil { /* ... */ }
defer c.Close()

asset, err := c.CreateAsset(ctx, sdk.CreateAssetRequest{Name: "sensor-9"})
// ...

sub, err := c.SubscribeMetaChanges(func(ev sdk.MetaChangeEvent) {
    var a sdk.Asset
    _ = ev.DecodeAfter(&a)
    log.Printf("%s: %s", ev.EventType, a.Name)
})
defer sub.Unsubscribe()
```

## Examples

See [`examples/`](examples) — three runnable adapters mirroring the Python
examples in [`adapters/python/examples`](../../python/examples):

- `temp_sensor` — simple collect loop
- `vibration_sensor` — multi-tag periodic publishing
- `temp_sensor_with_recovery` — device connect / health / reconnect hooks

Run any example with the EDG Core started locally:

```bash
go run github.com/e7217/edg/adapters/go/sdk/examples/temp_sensor
```
