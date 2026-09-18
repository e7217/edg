// Command opcua_sensor is a reference OPC UA adapter. It reads a set of
// variables from one OPC UA server on every poll and publishes them to EDG.
//
//	go run . [path/to/config.yaml]
//
// With asset_id set and no nodes, the variables to read are the point list
// EDG declares for that asset -- each point's address is a NodeId -- and the
// adapter rebuilds itself when the list changes (ADR 0011).
//
// Security mode None only, with anonymous or username login. Signing and
// encryption need certificates provisioned on both ends; copy this example and
// extend ConnectDevice when a server requires them.
package main

import (
	"context"
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
		AdapterVersion:  "opcua-example",
		Metadata:        map[string]string{"protocol": ProtocolOPCUA},
	}
	if acfg.AssetID == "" {
		acfg.AssetID = "opcua-" + sanitize(cfg.Endpoint)
	}

	if cfg.Provisioned() {
		err = sdk.RunProvisioned(ctx, sdk.ProvisionedConfig{Adapter: acfg}, func(pl *sdk.PointList) (sdk.Collector, error) {
			nodes, err := nodesFromPoints(pl)
			if err != nil {
				return nil, err
			}
			return NewDevice(cfg, nodes), nil
		})
	} else {
		err = sdk.NewAdapter(acfg, NewDevice(cfg, cfg.Nodes)).Run(ctx)
	}
	if err != nil {
		log.Fatalf("adapter exited: %v", err)
	}
}

// sanitize turns an endpoint into something usable in an asset id.
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml")
}
