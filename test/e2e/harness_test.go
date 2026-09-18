//go:build e2e

// Package e2e runs the real edg-core binary against a real VictoriaMetrics and
// checks what ends up stored, sample by sample.
//
// It exists because every sink test before it used a mock HTTP server: they
// proved a request was sent, not that the right samples were stored. The
// 2026-09-14 review found the gap -- test_pipeline.sh passed on
// `"status":"success"`, which an empty result also returns.
//
// Run it with scripts/e2e-storage.sh, which builds the binary and sets
// EDG_E2E_CORE_BIN. It needs Docker for VictoriaMetrics.
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

const httpToken = "e2e-token"

// vmImage is the VictoriaMetrics the release bundle ships (release.yml).
var vmImage = envOr("EDG_E2E_VM_IMAGE", "victoriametrics/victoria-metrics:v1.133.0")

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func httpOK(u string) bool {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+httpToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode/100 == 2
}

// --- VictoriaMetrics ---

type victoria struct {
	t    *testing.T
	name string
	port int
}

// VMFlags are the storage flags the shipped deployments pass. The harness must
// run what we ship, not a friendlier configuration.
var VMFlags = vmFlags()

func vmFlags() []string {
	flags := []string{"-retentionPeriod=100y"}
	// EDG_E2E_VM_NO_DEDUP=1 runs without the flag, to show what it prevents.
	if os.Getenv("EDG_E2E_VM_NO_DEDUP") != "1" {
		flags = append(flags, shippedDedupFlag)
	}
	return flags
}

// shippedDedupFlag is the flag every shipped deployment must pass. The e2e
// run proves it is needed; assertShipped proves it is still shipped.
const shippedDedupFlag = "-dedup.minScrapeInterval=1ms"

// assertShipped fails if a deployment has lost a flag the harness relies on,
// so the storage behaviour tested here cannot drift from what is installed.
func assertShipped(t *testing.T) {
	t.Helper()
	for _, f := range []string{"../../deploy/docker/compose.yml", "../../scripts/install.sh"} {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		require.Contains(t, string(b), shippedDedupFlag, "%s no longer passes %s", f, shippedDedupFlag)
	}
}

func startVictoria(t *testing.T) *victoria {
	t.Helper()
	assertShipped(t)
	v := &victoria{t: t, name: fmt.Sprintf("edg-e2e-vm-%d", os.Getpid()), port: freePort(t)}
	args := append([]string{"run", "-d", "--name", v.name,
		"-p", fmt.Sprintf("127.0.0.1:%d:8428", v.port), vmImage}, VMFlags...)
	out, err := exec.Command("docker", args...).CombinedOutput()
	require.NoError(t, err, string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", v.name).Run() })
	v.waitHealthy()
	return v
}

func (v *victoria) url() string { return fmt.Sprintf("http://127.0.0.1:%d", v.port) }

func (v *victoria) waitHealthy() {
	waitFor(v.t, "VictoriaMetrics /health", 60*time.Second, func() bool { return httpOK(v.url() + "/health") })
}

func (v *victoria) stop() {
	out, err := exec.Command("docker", "stop", v.name).CombinedOutput()
	require.NoError(v.t, err, string(out))
}

func (v *victoria) start() {
	out, err := exec.Command("docker", "start", v.name).CombinedOutput()
	require.NoError(v.t, err, string(out))
	v.waitHealthy()
}

// series is one exported time series.
type series struct {
	Metric     map[string]string `json:"metric"`
	Values     []float64         `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

// export returns every stored sample matching selector, after forcing
// VictoriaMetrics to make recent writes searchable.
func (v *victoria) export(selector string) []series {
	v.t.Helper()
	resp, err := http.Get(v.url() + "/internal/force_flush")
	require.NoError(v.t, err)
	resp.Body.Close()

	resp, err = http.Get(v.url() + "/api/v1/export?match[]=" + url.QueryEscape(selector))
	require.NoError(v.t, err)
	defer resp.Body.Close()
	require.Equal(v.t, http.StatusOK, resp.StatusCode)

	var out []series
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var s series
		require.NoError(v.t, json.Unmarshal(sc.Bytes(), &s))
		out = append(out, s)
	}
	require.NoError(v.t, sc.Err())
	return out
}

func (v *victoria) sampleCount(selector string) int {
	n := 0
	for _, s := range v.export(selector) {
		n += len(s.Values)
	}
	return n
}

// --- edg-core ---

type core struct {
	t           *testing.T
	bin, dir    string
	configPath  string
	natsPort    int
	httpPort    int
	metricsPort int
	cmd         *exec.Cmd
	log         *bytes.Buffer
}

const coreConfig = `
nats:
  host: 127.0.0.1
  port: %[1]d
  http_host: 127.0.0.1
  http_port: %[2]d
  auth:
    mode: compat
  store_dir: %[3]s/jetstream
  log_level: info
storage:
  metadata_db: %[3]s/metadata.db
  data_dir: %[3]s
  migrations_dir: embedded
  auto_migrate: true
templates:
  dir: %[3]s/templates
unknown_asset_policy: pass_through
data_contract:
  mode: enforce
http:
  enabled: true
  address: 127.0.0.1:%[4]d
  token_env: EDG_HTTP_TOKEN
  webui_enabled: false
metrics:
  enabled: true
  address: 127.0.0.1:%[5]d
sink:
  enabled: true
  url: %[6]s
  consumer_name: e2e-sink
  measurement: edg_data
  batch_max_size: 500
  flush_interval: 200ms
  request_timeout: 2s
  consumer_stat_interval: 200ms
`

func newCore(t *testing.T, vmURL string) *core {
	t.Helper()
	bin := os.Getenv("EDG_E2E_CORE_BIN")
	if bin == "" {
		t.Skip("EDG_E2E_CORE_BIN is not set; run scripts/e2e-storage.sh")
	}
	c := &core{t: t, bin: bin, dir: t.TempDir(),
		natsPort: freePort(t), httpPort: freePort(t), metricsPort: freePort(t)}
	natsMonitor := freePort(t)

	tdir := filepath.Join(c.dir, "templates")
	require.NoError(t, os.MkdirAll(tdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tdir, "equipment.yaml"),
		[]byte("name: equipment\nresources: []\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tdir, "pump-sensor.yaml"), []byte(`name: pump-sensor
resources:
  - name: temperature
    valueType: NUMBER
    unit: "°C"
  - name: state
    valueType: TEXT
  - name: running
    valueType: FLAG
`), 0o644))

	c.configPath = filepath.Join(c.dir, "config.yaml")
	require.NoError(t, os.WriteFile(c.configPath, []byte(fmt.Sprintf(coreConfig,
		c.natsPort, natsMonitor, c.dir, c.httpPort, c.metricsPort, vmURL)), 0o644))
	// Registered before start: a start that times out must not leak a core
	// holding its ports into the next run.
	t.Cleanup(c.stop)
	c.start()
	return c
}

func (c *core) start() {
	if c.log == nil {
		c.log = &bytes.Buffer{}
	}
	c.cmd = exec.Command(c.bin, "-config", c.configPath)
	c.cmd.Dir = c.dir
	c.cmd.Env = append(os.Environ(), "EDG_HTTP_TOKEN="+httpToken)
	c.cmd.Stdout = c.log
	c.cmd.Stderr = c.log
	require.NoError(c.t, c.cmd.Start())
	waitFor(c.t, "edg-core HTTP API", 30*time.Second, func() bool {
		return httpOK(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", c.httpPort))
	})
}

// stop is a graceful SIGTERM, which is what systemd and docker send.
func (c *core) stop() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
	if c.t.Failed() {
		c.t.Logf("--- edg-core log ---\n%s", tail(c.log.String(), 60))
	}
	c.cmd = nil
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (c *core) api(method, path string, body any) {
	c.t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(c.t, err)
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", c.httpPort, path), bytes.NewReader(payload))
	require.NoError(c.t, err)
	req.Header.Set("Authorization", "Bearer "+httpToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	require.True(c.t, resp.StatusCode/100 == 2, "%s %s: %d %s", method, path, resp.StatusCode, b)
}

func (c *core) connect() *nats.Conn {
	c.t.Helper()
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", c.natsPort))
	require.NoError(c.t, err)
	c.t.Cleanup(nc.Close)
	return nc
}

// metric returns the sum of every sample of a family, optionally filtered by
// a label fragment such as `reason="type_mismatch"`.
func (c *core) metric(family, labelFilter string) float64 {
	c.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", c.metricsPort))
	require.NoError(c.t, err)
	defer resp.Body.Close()
	sum := 0.0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, family) || strings.HasPrefix(line, "#") {
			continue
		}
		rest := line[len(family):]
		if rest[0] != ' ' && rest[0] != '{' {
			continue // a longer family name sharing the prefix
		}
		if labelFilter != "" && !strings.Contains(rest, labelFilter) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		require.NoError(c.t, err)
		sum += v
	}
	return sum
}
