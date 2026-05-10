// Command vibration_sensor is a virtual vibration sensor publishing velocity,
// acceleration, displacement and an alarm flag every 100ms. Mirror of the
// Python example adapters/python/examples/vibration_sensor.py.
package main

import (
	"context"
	"log"
	"math"
	"math/rand/v2"
	"os/signal"
	"syscall"
	"time"

	"github.com/e7217/edg/adapters/go/sdk"
)

type vibrationSensor struct {
	start time.Time
}

func (v *vibrationSensor) Collect(_ context.Context) ([]sdk.TagValue, error) {
	t := time.Since(v.start).Seconds()
	base := math.Sin(t * 2 * math.Pi * 0.5)

	velocity := round(2.0+base*1.5+(rand.Float64()-0.5)*0.6, 2)
	acceleration := round(0.5+math.Abs(base)*0.3+(rand.Float64()-0.5)*0.1, 3)
	displacement := round(50+base*20+(rand.Float64()-0.5)*10, 1)
	alarm := velocity > 3.5

	return []sdk.TagValue{
		{Name: "velocity", Quality: sdk.QualityGood, Number: &velocity, Unit: "mm/s"},
		{Name: "acceleration", Quality: sdk.QualityGood, Number: &acceleration, Unit: "g"},
		{Name: "displacement", Quality: sdk.QualityGood, Number: &displacement, Unit: "μm"},
		{Name: "alarm", Quality: sdk.QualityGood, Flag: &alarm},
	}, nil
}

func round(f float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(f*p) / p
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := sdk.NewAdapter(sdk.AdapterConfig{
		AssetID:         "vibration-sensor-001",
		CollectInterval: 100 * time.Millisecond,
		Metadata:        map[string]string{"location": "motor-1", "protocol": "virtual"},
	}, &vibrationSensor{start: time.Now()})

	if err := a.Run(ctx); err != nil {
		log.Fatalf("adapter exited: %v", err)
	}
}
