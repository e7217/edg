package sdk

import (
	"math/rand/v2"
	"time"
)

// Backoff computes exponential backoff delays with optional jitter.
// Delay for attempt n (zero-based) is: min(Base * 2^n, Max), then optionally
// scaled by 1 ± Jitter. Mirrors the Python SDK's BackoffStrategy.
type Backoff struct {
	// Base is the delay for attempt 0.
	Base time.Duration
	// Max caps the computed delay.
	Max time.Duration
	// Jitter, in [0.0, 1.0], adds randomness to prevent thundering herd.
	Jitter float64
}

// DefaultBackoff is used when AdapterConfig.Backoff is the zero value.
func DefaultBackoff() Backoff {
	return Backoff{Base: 1 * time.Second, Max: 60 * time.Second, Jitter: 0.1}
}

// NextDelay returns the delay for the given attempt number (zero-based).
// Negative attempts are clamped to 0. Jitter values outside [0, 1] are clamped
// to that range. Zero or negative Base returns Max.
func (b Backoff) NextDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	jitter := b.Jitter
	switch {
	case jitter < 0:
		jitter = 0
	case jitter > 1:
		jitter = 1
	}

	// Saturate the shift so attempt=63 doesn't roll over to 0. Base <= 0 is
	// degenerate; return Max.
	var d time.Duration
	if b.Base <= 0 || attempt >= 62 {
		d = b.Max
	} else {
		d = b.Base << uint(attempt)
		if d <= 0 || d > b.Max {
			d = b.Max
		}
	}

	if jitter > 0 {
		amount := time.Duration(float64(d) * jitter)
		offset := time.Duration((rand.Float64()*2 - 1) * float64(amount))
		d += offset
		if d < 0 {
			d = 0
		}
		if d > b.Max {
			d = b.Max
		}
	}
	return d
}
