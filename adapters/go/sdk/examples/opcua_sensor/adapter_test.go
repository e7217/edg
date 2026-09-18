package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"

	"github.com/e7217/edg/adapters/go/sdk"
)

// startServer runs an in-process OPC UA server with a namespace holding one
// variable of each kind the adapter maps, and returns its endpoint and the
// namespace index.
func startServer(t *testing.T) (string, uint16) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	s := server.New(
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
		server.EndPoint("127.0.0.1", port),
	)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start opc ua server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ns := server.NewNodeNameSpace(s, "EDGTest")
	ns.AddNewVariableStringNode("Temperature", float64(23.5))
	ns.AddNewVariableStringNode("Pressure", int32(7))
	ns.AddNewVariableStringNode("Running", true)
	ns.AddNewVariableStringNode("State", "RUNNING")
	return fmt.Sprintf("opc.tcp://127.0.0.1:%d", port), ns.ID()
}

func node(t *testing.T, name, nodeID, unit string) Node {
	t.Helper()
	n := []Node{{Name: name, NodeID: nodeID, Unit: unit}}
	if err := parseNodes(n); err != nil {
		t.Fatal(err)
	}
	return n[0]
}

func TestDeviceReadsEveryKind(t *testing.T) {
	endpoint, ns := startServer(t)
	id := func(s string) string { return fmt.Sprintf("ns=%d;s=%s", ns, s) }
	cfg := &Config{Endpoint: endpoint, Timeout: 5}
	d := NewDevice(cfg, []Node{
		node(t, "temperature", id("Temperature"), "°C"),
		node(t, "pressure", id("Pressure"), "bar"),
		node(t, "running", id("Running"), ""),
		node(t, "state", id("State"), ""),
		node(t, "missing", id("NoSuchNode"), ""),
	})
	ctx := context.Background()
	if err := d.ConnectDevice(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.DisconnectDevice(ctx)
	if err := d.CheckDeviceHealth(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := d.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]sdk.TagValue{}
	for _, v := range got {
		byName[v.Name] = v
	}
	if len(got) != 4 {
		t.Fatalf("got %d values, want 4 (a Bad node yields none): %+v", len(got), got)
	}
	if v := byName["temperature"]; v.Number == nil || *v.Number != 23.5 || v.Unit != "°C" || v.Quality != sdk.QualityGood {
		t.Errorf("temperature = %+v", v)
	}
	if v := byName["pressure"]; v.Number == nil || *v.Number != 7 {
		t.Errorf("pressure = %+v", v)
	}
	if v := byName["running"]; v.Flag == nil || !*v.Flag {
		t.Errorf("running = %+v", v)
	}
	if v := byName["state"]; v.Text == nil || *v.Text != "RUNNING" {
		t.Errorf("state = %+v", v)
	}
}

func TestConnectFailureIsADeviceError(t *testing.T) {
	d := NewDevice(&Config{Endpoint: "opc.tcp://127.0.0.1:1", Timeout: 1}, nil)
	err := d.ConnectDevice(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("err = %v", err)
	}
	if !errorsIsDevice(err) {
		t.Fatalf("a failed connect must be sdk.ErrDeviceConnection so the SDK backs off and retries: %v", err)
	}
}

// The adapter end to end through the SDK: collected values reach NATS.
func TestAdapterPublishes(t *testing.T) {
	endpoint, ns := startServer(t)
	natsURL := startNATS(t)
	got := watch(t, natsURL)

	cfg := &Config{Endpoint: endpoint, Timeout: 5}
	d := NewDevice(cfg, []Node{node(t, "temperature", fmt.Sprintf("ns=%d;s=Temperature", ns), "°C")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sdk.NewAdapter(sdk.AdapterConfig{AssetID: "press-01", NATSURL: natsURL,
			CollectInterval: 50 * time.Millisecond, DisableStatusReporting: true}, d).Run(ctx)
	}()
	select {
	case ad := <-got:
		if ad.AssetID != "press-01" || len(ad.Values) != 1 || *ad.Values[0].Number != 23.5 {
			t.Fatalf("published %+v", ad)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing published")
	}
}

func TestNodesFromPoints(t *testing.T) {
	nodes, err := nodesFromPoints(&sdk.PointList{Protocol: ProtocolOPCUA, Points: []sdk.Point{
		{Name: "temperature", Address: "ns=2;s=Temperature", Unit: "°C", Enabled: true},
		{Name: "flow", Address: "ns=2;i=1001", Enabled: true},
		{Name: "retired", Address: "ns=2;s=Old", Enabled: false},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].id == nil || nodes[1].NodeID != "ns=2;i=1001" {
		t.Fatalf("nodes = %+v", nodes)
	}
	for want, pl := range map[string]*sdk.PointList{
		"not an OPC UA NodeId": {Points: []sdk.Point{{Name: "p", Address: "40001", Enabled: true}}},
		"this adapter reads":   {Protocol: "modbus-tcp"},
	} {
		if _, err := nodesFromPoints(pl); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}
