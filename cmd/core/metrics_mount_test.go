package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/e7217/edg/internal/core"
	"github.com/e7217/edg/internal/metrics"
)

// startMonitoredServer boots an embedded NATS server with its monitoring port
// enabled on a free loopback port.
func startMonitoredServer(t *testing.T) (*server.Server, int) {
	t.Helper()
	httpPort, clientPort := freePort(t), freePort(t)
	ns, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      clientPort,
		HTTPHost:  "127.0.0.1",
		HTTPPort:  httpPort,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns, httpPort
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func get(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

// TestMountOnMonitorMux is the guard behind the one undocumented assumption in
// this feature: nats-server's monitoring handler is an *http.ServeMux we can
// add a route to. If an upgrade changes that, /metrics would silently vanish;
// this test turns that into a build failure.
func TestMountOnMonitorMux(t *testing.T) {
	ns, port := startMonitoredServer(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// The path must be free before we take it. If nats-server ever ships its
	// own /metrics, mux.Handle would panic or we would shadow theirs.
	if code, _, _ := get(t, base+metricsPath); code != http.StatusNotFound {
		t.Fatalf("%s is already served by nats-server (status %d); the mount would collide", metricsPath, code)
	}

	r := metrics.NewRegistry()
	r.NewCounter(metrics.Desc{Name: "edg_test_probe_total", Help: "h."}).Inc()
	if err := mountMetricsOnMonitor(ns, metrics.Handler(r)); err != nil {
		t.Fatalf("mount: %v", err)
	}

	code, header, body := get(t, base+metricsPath)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := header.Get("Content-Type"); got != metrics.ContentType {
		t.Errorf("Content-Type = %q, want %q", got, metrics.ContentType)
	}
	if !strings.Contains(body, "edg_test_probe_total 1") {
		t.Errorf("body does not carry the registry:\n%s", body)
	}

	// The neighbouring endpoints must still work: we added a route, not a
	// handler replacement.
	if code, _, _ := get(t, base+"/varz"); code != http.StatusOK {
		t.Errorf("/varz broke after mounting: status %d", code)
	}
	if code, _, _ := get(t, base+"/debug/vars"); code != http.StatusOK {
		t.Errorf("/debug/vars broke after mounting: status %d", code)
	}
}

func TestMountRequiresMonitoringPort(t *testing.T) {
	ns, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: freePort(t), NoLog: true, NoSigs: true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		t.Fatal("server not ready")
	}
	defer ns.Shutdown()

	if err := mountMetricsOnMonitor(ns, http.NotFoundHandler()); err == nil {
		t.Error("mounting succeeded with no monitoring port configured")
	}
}

// A metrics port that cannot bind must be a log line, not a dead gateway.
func TestMetricsListenerBindFailureIsNotFatal(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer occupied.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startMetricsListener(ctx, occupied.Addr().String(), http.NotFoundHandler()) // must return
}

func TestMetricsListenerServesAndShutsDown(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	r := metrics.NewRegistry()
	r.NewCounter(metrics.Desc{Name: "edg_test_probe_total", Help: "h."}).Add(42)

	ctx, cancel := context.WithCancel(context.Background())
	startMetricsListener(ctx, addr, metrics.Handler(r))

	url := "http://" + addr + metricsPath
	var body string
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "edg_test_probe_total 42") {
		t.Fatalf("listener did not serve the registry, got:\n%s", body)
	}

	cancel()
	for i := 0; i < 100; i++ {
		if _, err := http.Get(url); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("listener still answering after its context was cancelled")
}

// The NATS collectors must report the embedded server, and the JetStream ones
// must move when data is stored.
func TestNATSCollectors(t *testing.T) {
	ns, _ := startMonitoredServer(t)
	cfg := core.DefaultCoreConfig()

	r := metrics.NewRegistry()
	registerNATSCollectors(r, ns, cfg)
	out := string(r.Gather())

	for _, name := range []string{
		"edg_core_nats_connections",
		"edg_core_nats_subscriptions",
		"edg_core_nats_stalled_clients",
		"edg_core_nats_slow_consumers_total",
		"edg_core_js_messages",
		"edg_core_js_bytes",
		"edg_core_js_store_bytes",
		"edg_core_js_memory_bytes",
		"edg_core_js_streams",
		"edg_core_js_consumers",
		"edg_core_js_stream_max_bytes",
	} {
		if !strings.Contains(out, name+" ") {
			t.Errorf("%s missing:\n%s", name, out)
		}
	}

	// ADR 0001 fixes the stream ceiling at 1 GiB; the metric must report the
	// configured value, not a Jsz-derived guess.
	want := fmt.Sprintf("edg_core_js_stream_max_bytes %d", cfg.JetStream.Stream.MaxBytes)
	if !strings.Contains(out, want) {
		t.Errorf("%s missing:\n%s", want, out)
	}
}

// internal/metrics must not depend on nats-server: the NATS collectors live in
// this package precisely so that the metrics package stays free of it.
func TestMetricsPackageDoesNotImportNATS(t *testing.T) {
	deps := packageDeps(t, "github.com/e7217/edg/internal/metrics")
	for _, dep := range deps {
		if strings.Contains(dep, "nats-io") {
			t.Errorf("internal/metrics depends on %s", dep)
		}
	}
}
