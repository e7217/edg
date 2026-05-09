// Command temp_sensor is a virtual temperature/humidity sensor that publishes
// readings to EDG Core every second. Mirror of the Python example
// adapters/python/examples/temp_sensor.py.
package main

import (
	"context"
	"log"
	"math/rand/v2"
	"os/signal"
	"syscall"
	"time"

	"github.com/e7217/edg/adapters/go/sdk"
)

type tempSensor struct{}

func (tempSensor) Collect(_ context.Context) ([]sdk.TagValue, error) {
	temp := 20 + rand.Float64()*10
	humidity := 40 + rand.Float64()*30
	return []sdk.TagValue{
		{Name: "temperature", Quality: sdk.QualityGood, Number: &temp, Unit: "°C"},
		{Name: "humidity", Quality: sdk.QualityGood, Number: &humidity, Unit: "%"},
	}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := sdk.NewAdapter(sdk.AdapterConfig{
		AssetID:         "temp-sensor-001",
		CollectInterval: time.Second,
		Metadata:        map[string]string{"location": "factory-a", "protocol": "virtual"},
	}, tempSensor{})

	if err := a.Run(ctx); err != nil {
		log.Fatalf("adapter exited: %v", err)
	}
}
