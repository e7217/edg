// Command melsec_mc_sensor is a reference adapter for Mitsubishi MELSEC PLCs
// (Q, L, iQ-R, and FX5 with MC protocol enabled). It reads devices with the MC
// protocol, 3E frame in binary code, over TCP, and publishes them to EDG.
//
//	go run . [path/to/config.yaml]
//
// With asset_id set and no points, the devices to read are the point list EDG
// declares for that asset -- each point's address is a device such as D100 --
// and the adapter rebuilds itself when the list changes (ADR 0011).
//
// Enable the MC protocol on the PLC's Ethernet port (communication data code:
// binary) and open a TCP port for it; that port goes in the config.
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
	path := defaultConfigPath()
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		log.Fatalf("load config %s: %v", path, err)
	}
	if url := os.Getenv("EDG_NATS_URL"); url != "" {
		cfg.NATSURL = url
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	acfg := sdk.AdapterConfig{
		AssetID:         cfg.AssetID,
		NATSURL:         cfg.NATSURL,
		CollectInterval: time.Duration(cfg.PollInterval * float64(time.Second)),
		AdapterVersion:  "melsec-mc-example",
		Metadata:        map[string]string{"protocol": ProtocolMELSEC},
	}
	if acfg.AssetID == "" {
		acfg.AssetID = fmt.Sprintf("melsec-%s-%d", cfg.Host, cfg.Port)
	}

	if cfg.Provisioned() {
		err = sdk.RunProvisioned(ctx, sdk.ProvisionedConfig{Adapter: acfg}, func(pl *sdk.PointList) (sdk.Collector, error) {
			specs, err := specsFromPointList(pl)
			if err != nil {
				return nil, err
			}
			return NewPLC(cfg, specs), nil
		})
	} else {
		err = sdk.NewAdapter(acfg, NewPLC(cfg, cfg.specs)).Run(ctx)
	}
	if err != nil {
		log.Fatalf("adapter exited: %v", err)
	}
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml")
}
