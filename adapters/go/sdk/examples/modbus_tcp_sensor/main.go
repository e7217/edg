// Command modbus_tcp_sensor is a reference Modbus TCP adapter. Edit
// mapping.yaml (or pass a custom path) and run:
//
//	go run . [path/to/mapping.yaml]
//
// The adapter reads each configured register over Modbus TCP and
// publishes the decoded values to EDG Core via NATS.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/e7217/edg/adapters/go/sdk"
)

func main() {
	cfgPath := defaultMappingPath()
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("load config %s: %v", cfgPath, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dev := NewModbusDevice(cfg)
	a := sdk.NewAdapter(sdk.AdapterConfig{
		AssetID:         fmt.Sprintf("modbus-%s-%d", cfg.Host, cfg.UnitID),
		CollectInterval: time.Duration(cfg.PollInterval * float64(time.Second)),
		Metadata: map[string]string{
			"protocol": "modbus-tcp",
			"host":     cfg.Host,
			"unit_id":  fmt.Sprintf("%d", cfg.UnitID),
		},
	}, dev)

	if err := a.Run(ctx); err != nil {
		log.Fatalf("adapter exited: %v", err)
	}
}

func defaultMappingPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "mapping.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "mapping.yaml")
}
