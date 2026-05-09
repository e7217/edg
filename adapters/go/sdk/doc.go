// Package sdk provides a Go SDK for building EDG Platform adapters.
//
// Adapters collect data from sensors or upstream systems and publish it to the
// EDG Core over NATS. The SDK wraps the NATS subject contract documented at
// docs/ADAPTER_GUIDE.md and docs/events.md so adapter authors do not need to
// hand-roll wire types, request/reply, or reconnection logic.
//
// # Quick start
//
//	package main
//
//	import (
//	    "context"
//	    "os/signal"
//	    "syscall"
//
//	    "github.com/e7217/edg/adapters/go/sdk"
//	)
//
//	type tempSensor struct{}
//
//	func (tempSensor) Collect(ctx context.Context) ([]sdk.TagValue, error) {
//	    n := 25.5
//	    return []sdk.TagValue{{Name: "temperature", Number: &n, Unit: "°C", Quality: sdk.QualityGood}}, nil
//	}
//
//	func main() {
//	    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
//	    defer stop()
//
//	    a := sdk.NewAdapter(sdk.AdapterConfig{AssetID: "temp-001"}, tempSensor{})
//	    _ = a.Run(ctx)
//	}
package sdk
