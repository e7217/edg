package sdk

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

// startRestrictedServer boots a server where the only user may publish
// telemetry but not mutate master data, mirroring the adapter role from
// ADR 0007.
func startRestrictedServer(t *testing.T) (url, user, pass string) {
	t.Helper()

	user, pass = "adapter", "kJ8_xQ2mNpR7vT4wY1zA6bC3dE5fG9hI0jK2lM4nO6p"
	opts := &natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
		Users: []*natsserver.User{{
			Username: user,
			Password: pass,
			Permissions: &natsserver.Permissions{
				Publish: &natsserver.SubjectPermission{
					Allow: []string{SubjectAssetData, SubjectAssetList},
					Deny:  []string{SubjectAssetDelete, SubjectAssetCreate},
				},
				Subscribe: &natsserver.SubjectPermission{
					Allow: []string{"platform.data.>", "_INBOX.>"},
					Deny:  []string{"platform.meta.>"},
				},
			},
		}},
	}
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(ns.Shutdown)

	return "nats://" + user + ":" + pass + "@" + ns.Addr().String(), user, pass
}

// TestCredentialsInURLAuthenticate pins the decision not to add credential
// fields to the SDK: a URL carrying userinfo is the whole integration. The
// secrets natsauth generates are base64url, so they never need escaping here.
func TestCredentialsInURLAuthenticate(t *testing.T) {
	url, _, _ := startRestrictedServer(t)

	c := NewClient(Options{URL: url, Name: "probe"})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("credentials in URL were rejected: %v", err)
	}
	defer c.Close()

	n := 25.5
	err := c.PublishAssetData(context.Background(), AssetData{
		AssetID: "s1",
		Values:  []TagValue{{Name: "t", Number: &n, Quality: QualityGood}},
	})
	if err != nil {
		t.Fatalf("allowed publish failed: %v", err)
	}
}

// TestDeniedRequestReportsForbidden is the reason this file exists. Without
// correlation, a denied request/reply is indistinguishable from a dead core:
// the publish is dropped by the server, no reply ever comes, and the caller
// sees only "context deadline exceeded" after the full request timeout.
func TestDeniedRequestReportsForbidden(t *testing.T) {
	url, _, _ := startRestrictedServer(t)

	c := NewClient(Options{URL: url, Name: "probe", RequestTimeout: 1500 * time.Millisecond})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	err := c.DeleteAsset(context.Background(), DeleteAssetRequest{ID: "s1"})
	if err == nil {
		t.Fatal("expected an error for a denied subject")
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("denied request must report ErrForbidden, got %v", err)
	}
	if !strings.Contains(err.Error(), SubjectAssetDelete) {
		t.Errorf("error must name the denied subject, got %q", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permission") {
		t.Errorf("error must say why, got %q", err)
	}
}

// TestAllowedRequestUnaffected proves the correlation does not mislabel an
// ordinary timeout (core down, slow reply) as a permission problem.
func TestAllowedRequestUnaffected(t *testing.T) {
	url, _, _ := startRestrictedServer(t)

	c := NewClient(Options{URL: url, Name: "probe", RequestTimeout: 500 * time.Millisecond})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// SubjectAssetList is allowed to publish, but nothing answers it here.
	_, err := c.ListAssets(context.Background())
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatalf("an unanswered but permitted subject must not be reported as forbidden: %v", err)
	}
}

// TestDeniedSubscribeIsReported documents the one case that cannot be made
// synchronous. nc.Subscribe is fire-and-forget and the server's -ERR arrives
// through nats.go's asynchronous callback queue, which is not ordered against
// Flush, so SubscribeMetaChanges cannot return the refusal. (nats.go's
// PermissionErrOnSubscribe only covers SubscribeSync — see its doc comment.)
// A denied subscription otherwise has no symptom at all: the adapter simply
// never receives anything. The contract is therefore that it is logged with
// enough detail to act on.
func TestDeniedSubscribeIsReported(t *testing.T) {
	url, _, _ := startRestrictedServer(t)

	var buf safeBuffer
	c := NewClient(Options{URL: url, Name: "probe", Logger: newTestLogger(&buf)})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.SubscribeMetaChanges(func(MetaChangeEvent) {}); err != nil {
		t.Fatalf("subscribe returned an error it cannot actually detect: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "nats permission denied") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	logged := buf.String()
	if !strings.Contains(logged, "nats permission denied") {
		t.Fatalf("a denied subscription must be logged distinctly, got: %s", logged)
	}
	if !strings.Contains(logged, SubjectMetaChangedAll) {
		t.Errorf("the log must name the subject, got: %s", logged)
	}
	if !strings.Contains(logged, "subscription") {
		t.Errorf("the log must name the operation, got: %s", logged)
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"nats://localhost:4222", "nats://localhost:4222"},
		{"nats://adapter:s3cr3t@localhost:4222", "nats://adapter:xxxxx@localhost:4222"},
		{"nats://adapter@localhost:4222", "nats://adapter@localhost:4222"},
		{
			"nats://a:b@h1:4222,nats://a:b@h2:4222",
			"nats://a:xxxxx@h1:4222,nats://a:xxxxx@h2:4222",
		},
		{"://not a url", "://not a url"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := redactURL(tt.in); got != tt.want {
			t.Errorf("redactURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestConnectDoesNotLogSecret guards the log path directly: putting
// credentials in the URL is only safe if the URL is never echoed verbatim.
func TestConnectDoesNotLogSecret(t *testing.T) {
	url, _, pass := startRestrictedServer(t)

	var buf safeBuffer
	c := NewClient(Options{URL: url, Name: "probe", Logger: newTestLogger(&buf)})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if strings.Contains(buf.String(), pass) {
		t.Fatalf("connection log leaked the password: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "xxxxx") {
		t.Errorf("expected a redacted URL in the log, got: %s", buf.String())
	}
}

// safeBuffer is a concurrency-safe sink for the slog output under test; the
// NATS callbacks that write to it run on their own goroutines.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
