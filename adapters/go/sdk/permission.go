package sdk

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// permissionWindow is how long a reported violation stays attributable to a
// waiting call. It only needs to outlive the gap between the server refusing a
// publish and the caller's request timing out, which is the request timeout
// itself; a generous window costs nothing because entries are keyed by subject.
const permissionWindow = 30 * time.Second

// maxTrackedViolations bounds the map. An adapter publishes a small fixed set
// of subjects, so this is only a guard against a pathological caller.
const maxTrackedViolations = 32

// permSubjectRe extracts the subject from a nats-server violation, e.g.
// `nats: Permissions Violation for Publish to "platform.meta.asset.delete"`.
var permSubjectRe = regexp.MustCompile(`Permissions Violation for (\w+) to "([^"]+)"`)

// permissionTracker records subjects the server has refused, so that a call
// blocked on a reply that will never come can explain itself.
type permissionTracker struct {
	mu   sync.Mutex
	seen map[string]permissionHit
}

type permissionHit struct {
	op string
	at time.Time
}

// record notes a violation and reports the operation and subject it applies
// to, so the caller can log something actionable. ok is false when errText is
// not a permission violation.
func (p *permissionTracker) record(errText string, now time.Time) (op, subject string, ok bool) {
	m := permSubjectRe.FindStringSubmatch(errText)
	if m == nil {
		return "", "", false
	}
	op, subject = strings.ToLower(m[1]), m[2]

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = make(map[string]permissionHit, 8)
	}
	if len(p.seen) >= maxTrackedViolations {
		p.seen = make(map[string]permissionHit, 8)
	}
	p.seen[subject] = permissionHit{op: op, at: now}
	return op, subject, true
}

// refused reports whether the subject was recently denied, consuming the entry
// so a later unrelated timeout on the same subject is not mislabeled.
func (p *permissionTracker) refused(subject string, now time.Time) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hit, ok := p.seen[subject]
	if !ok {
		return "", false
	}
	delete(p.seen, subject)
	if now.Sub(hit.at) > permissionWindow {
		return "", false
	}
	return hit.op, true
}

// isPermissionError reports whether an error from a synchronous NATS call is a
// permission refusal. Subscribe returns this directly once the connection is
// built with nats.PermissionErrOnSubscribe.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "permissions violation")
}

// redactURL replaces the password in any userinfo so a NATS URL can be logged.
// Credentials are carried in the URL (ADR 0007), which is only safe if the URL
// is never echoed verbatim. Unparseable input is returned unchanged rather than
// risking a partial leak from a half-successful parse.
func redactURL(raw string) string {
	if raw == "" {
		return raw
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		u, err := url.Parse(trimmed)
		if err != nil || u.User == nil {
			out = append(out, part)
			continue
		}
		if _, hasPassword := u.User.Password(); !hasPassword {
			out = append(out, part)
			continue
		}
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
		// url.String percent-encodes the placeholder's characters, none of
		// which need escaping, so this round-trips cleanly.
		out = append(out, u.String())
	}
	return strings.Join(out, ",")
}
