package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/adapters/go/sdk"
)

func errorsIsDevice(err error) bool { return errors.Is(err, sdk.ErrDevice) }

func startNATS(t *testing.T) string {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Port: -1})
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns.ClientURL()
}

func watch(t *testing.T, url string) <-chan sdk.AssetData {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	out := make(chan sdk.AssetData, 16)
	if _, err := nc.Subscribe(sdk.SubjectAssetData, func(m *nats.Msg) {
		var ad sdk.AssetData
		if json.Unmarshal(m.Data, &ad) == nil {
			select {
			case out <- ad:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	return out
}
