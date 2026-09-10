package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/e7217/edg/internal/core"
	"github.com/e7217/edg/internal/metrics"
)

// metricsPath is where the exposition is served on every mount point.
const metricsPath = "/metrics"

// registerRuntimeCollectors adds the process-level families: the Go runtime,
// /proc, the build stamp, and the embedded NATS server.
//
// The NATS collectors live here rather than in internal/metrics so that the
// metrics package never imports nats-server; a guard test asserts that.
func registerRuntimeCollectors(r *metrics.Registry, ns *server.Server, cfg core.CoreConfig) {
	r.RegisterRuntime()
	r.RegisterProcess()

	r.NewInfoGauge(metrics.Desc{
		Name: "edg_core_build_info",
		Help: "Build stamp of the running binary, always 1. Makes /api/v1/version queryable from the TSDB.",
	},
		"version", Version,
		"build_time", BuildTime,
		"git_commit", GitCommit,
		"go_version", runtime.Version(),
	)

	if ns != nil {
		registerNATSCollectors(r, ns, cfg)
	}
}

// registerNATSCollectors exposes the embedded server's own counters.
func registerNATSCollectors(r *metrics.Registry, ns *server.Server, cfg core.CoreConfig) {
	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_nats_connections",
		Help: "Client connections to the embedded NATS server.",
	}, func() float64 { return float64(ns.NumClients()) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_nats_subscriptions",
		Help: "Active subscriptions across all clients.",
	}, func() float64 { return float64(ns.NumSubscriptions()) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_nats_stalled_clients",
		Help: "Clients whose outbound queue is full and are being back-pressured.",
	}, func() float64 { return float64(ns.NumStalledClients()) })

	r.NewFuncCounter(metrics.Desc{
		Name: "edg_core_nats_slow_consumers_total",
		Help: "Slow-consumer events. Adapter-to-core delivery is plain NATS and best-effort (ADR 0001), so this is the data-loss signal for that hop.",
	}, func() float64 { return float64(ns.NumSlowConsumers()) })

	// JetStream account totals. Accounts/Streams/Consumer are all left false:
	// per-stream detail would add cardinality for a deployment that has one
	// stream, and it makes the call walk more state on every scrape.
	js := &jsSampler{ns: ns}
	js.refresh()
	r.BeforeGather(js.refresh)

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_messages",
		Help: "Messages held in JetStream.",
	}, func() float64 { return js.value(func(i *server.JSInfo) float64 { return float64(i.Messages) }) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_bytes",
		Help: "Bytes held in JetStream. Against edg_core_js_stream_max_bytes this is how close the stream is to discarding old data (ADR 0001).",
	}, func() float64 { return js.value(func(i *server.JSInfo) float64 { return float64(i.Bytes) }) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_store_bytes",
		Help: "Bytes JetStream has written to disk.",
	}, func() float64 { return js.value(func(i *server.JSInfo) float64 { return float64(i.Store) }) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_memory_bytes",
		Help: "Bytes JetStream holds in memory.",
	}, func() float64 { return js.value(func(i *server.JSInfo) float64 { return float64(i.Memory) }) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_streams",
		Help: "Streams on this server.",
	}, func() float64 { return js.value(func(i *server.JSInfo) float64 { return float64(i.Streams) }) })

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_consumers",
		Help: "Consumers on this server. The VM sink owns one durable pull consumer.",
	}, func() float64 { return js.value(func(i *server.JSInfo) float64 { return float64(i.Consumers) }) })

	// The limit comes from configuration rather than from Jsz: it is exact,
	// free, and it is the number ADR 0001 fixes at 1 GiB with DiscardOld.
	maxBytes := float64(cfg.JetStream.Stream.MaxBytes)
	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_js_stream_max_bytes",
		Help: "Configured jetstream.stream.max_bytes. Reaching it discards the oldest data (ADR 0001).",
	}, func() float64 { return maxBytes })
}

// jsSampler calls Jsz once per scrape. Jsz walks server state under internal
// locks, so calling it once per derived series would put scrape traffic on the
// data path.
type jsSampler struct {
	ns *server.Server
	// A scrape reads the snapshot while the BeforeGather hook may be
	// replacing it, so the pointer swap has to be atomic.
	info atomic.Pointer[server.JSInfo]
}

func (s *jsSampler) refresh() {
	info, err := s.ns.Jsz(&server.JSzOptions{})
	if err != nil || info == nil {
		return // keep the previous snapshot rather than reporting a false zero
	}
	s.info.Store(info)
}

func (s *jsSampler) value(pick func(*server.JSInfo) float64) float64 {
	info := s.info.Load()
	if info == nil {
		return 0
	}
	return pick(info)
}

// mountMetricsOnMonitor attaches /metrics to the embedded NATS monitoring mux,
// which serves /varz, /healthz and /debug/vars on nats.http_port.
//
// That the handler is an *http.ServeMux is an undocumented implementation
// detail of nats-server (server.go assigns one to s.httpHandler but the
// accessor returns http.Handler), so metrics_mount_test.go asserts it against
// a real server and fails the build on an upgrade that changes it.
func mountMetricsOnMonitor(ns *server.Server, h http.Handler) error {
	handler := ns.HTTPHandler()
	if handler == nil {
		return fmt.Errorf("monitoring endpoint is disabled; set nats.http_port")
	}
	mux, ok := handler.(*http.ServeMux)
	if !ok {
		return fmt.Errorf("nats-server monitoring handler is %T, not *http.ServeMux", handler)
	}
	mux.Handle(metricsPath, h)
	return nil
}

// startMetricsListener serves /metrics on its own address.
//
// This is the mount that matters for a container deployment: the monitoring
// port binds to loopback (ADR 0007), so a scraper in another container can
// only reach a listener of our own.
//
// A bind failure is reported, never fatal. An observability port must not stop
// an industrial gateway from moving data.
func startMetricsListener(ctx context.Context, addr string, h http.Handler) {
	mux := http.NewServeMux()
	mux.Handle(metricsPath, h)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[Core] metrics listener on %s unavailable: %v (metrics remain on the monitoring port)", addr, err)
		return
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[Core] metrics listener stopped: %v", err)
		}
	}()
}

// setUpMetrics registers the process collectors and mounts /metrics, returning
// the URLs it is reachable at.
//
// Nothing here is fatal. Metrics are how an operator learns the gateway is
// unwell; they are not a precondition for it running.
func setUpMetrics(ctx context.Context, ns *server.Server, cfg core.CoreConfig) []string {
	if !cfg.Metrics.Enabled {
		log.Print("[Core] metrics disabled by configuration")
		return nil
	}

	registerRuntimeCollectors(metrics.Default, ns, cfg)
	handler := metrics.Handler(metrics.Default)

	var urls []string
	if err := mountMetricsOnMonitor(ns, handler); err != nil {
		log.Printf("[Core] metrics not mounted on the monitoring port: %v", err)
	} else {
		urls = append(urls, fmt.Sprintf("http://%s:%d%s", cfg.NATS.HTTPHost, cfg.NATS.HTTPPort, metricsPath))
	}

	if cfg.Metrics.Address != "" {
		startMetricsListener(ctx, cfg.Metrics.Address, handler)
		urls = append(urls, fmt.Sprintf("http://%s%s", cfg.Metrics.Address, metricsPath))
	}

	if len(urls) == 0 {
		log.Print("[Core] metrics are enabled but reachable at no address; set metrics.address or nats.http_port")
	}
	return urls
}
