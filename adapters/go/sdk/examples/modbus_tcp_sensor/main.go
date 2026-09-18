// Command modbus_tcp_sensor is a reference Modbus adapter, over TCP or RTU
// (serial, transport: rtu). Edit
// mapping.yaml (or pass a custom path) and run:
//
//	go run . [path/to/mapping.yaml]
//
// The adapter reads each configured register over Modbus TCP and
// publishes the decoded values to EDG Core via NATS.
//
// With asset_id set and no registers, the register map is the point list EDG
// declares for that asset, and the adapter rebuilds itself when it changes
// (ADR 0011).
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
	if url := os.Getenv("EDG_NATS_URL"); url != "" {
		cfg.NATSURL = url
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	endpoint := cfg.Host
	if cfg.Transport == TransportRTU {
		endpoint = filepath.Base(cfg.Serial.Port)
	}
	acfg := sdk.AdapterConfig{
		AssetID:         fmt.Sprintf("modbus-%s-%d", endpoint, cfg.UnitID),
		NATSURL:         cfg.NATSURL,
		CollectInterval: time.Duration(cfg.PollInterval * float64(time.Second)),
		AdapterVersion:  "modbus-tcp-example",
		Metadata: map[string]string{
			"protocol": cfg.Protocol(),
			"host":     endpoint,
			"unit_id":  fmt.Sprintf("%d", cfg.UnitID),
		},
	}

	if cfg.Provisioned() {
		acfg.AssetID = cfg.AssetID
		err = sdk.RunProvisioned(ctx, sdk.ProvisionedConfig{Adapter: acfg}, func(pl *sdk.PointList) (sdk.Collector, error) {
			regs, err := registersFromPoints(pl, cfg.Protocol())
			if err != nil {
				return nil, err
			}
			device := *cfg
			device.Registers = regs
			return NewModbusDevice(&device), nil
		})
	} else {
		err = sdk.NewAdapter(acfg, NewModbusDevice(cfg)).Run(ctx)
	}
	if err != nil {
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
