package sdk

import (
	"testing"
	"time"
)

func TestBackoff_ExponentialNoJitter(t *testing.T) {
	b := Backoff{Base: time.Second, Max: 60 * time.Second}
	cases := map[int]time.Duration{
		0: 1 * time.Second,
		1: 2 * time.Second,
		2: 4 * time.Second,
		3: 8 * time.Second,
	}
	for attempt, want := range cases {
		got := b.NextDelay(attempt)
		if got != want {
			t.Errorf("NextDelay(%d) = %s, want %s", attempt, got, want)
		}
	}
}

func TestBackoff_ClampsToMax(t *testing.T) {
	b := Backoff{Base: time.Second, Max: 60 * time.Second}
	// 2^10 = 1024s without max
	if got := b.NextDelay(10); got > 60*time.Second {
		t.Errorf("NextDelay(10) = %s, want <= 60s", got)
	}
	// Saturating shift safety
	if got := b.NextDelay(100); got != 60*time.Second {
		t.Errorf("NextDelay(100) = %s, want 60s", got)
	}
}

func TestBackoff_NegativeAttemptTreatedAsZero(t *testing.T) {
	b := Backoff{Base: time.Second, Max: 60 * time.Second}
	if got := b.NextDelay(-1); got != time.Second {
		t.Errorf("NextDelay(-1) = %s, want 1s", got)
	}
}

func TestBackoff_MaxSmallerThanBase(t *testing.T) {
	b := Backoff{Base: 10 * time.Second, Max: 5 * time.Second}
	// Even first attempt clamps to Max.
	if got := b.NextDelay(0); got > 5*time.Second {
		t.Errorf("NextDelay(0) = %s, want <= 5s", got)
	}
}

func TestBackoff_JitterRange(t *testing.T) {
	b := Backoff{Base: time.Second, Max: 60 * time.Second, Jitter: 0.1}
	// 100 samples; every value within 1s ± 10%.
	low := time.Duration(float64(time.Second) * 0.9)
	high := time.Duration(float64(time.Second) * 1.1)
	for i := 0; i < 100; i++ {
		d := b.NextDelay(0)
		if d < low || d > high {
			t.Errorf("sample %d: %s out of [%s, %s]", i, d, low, high)
		}
	}
}

func TestBackoff_ZeroJitterDeterministic(t *testing.T) {
	b := Backoff{Base: time.Second, Max: 60 * time.Second, Jitter: 0}
	if got := b.NextDelay(0); got != time.Second {
		t.Errorf("NextDelay(0) = %s, want 1s", got)
	}
	if got := b.NextDelay(1); got != 2*time.Second {
		t.Errorf("NextDelay(1) = %s, want 2s", got)
	}
}

func TestBackoff_OutOfRangeJitterClamps(t *testing.T) {
	// Negative jitter is treated as 0 (deterministic).
	b1 := Backoff{Base: time.Second, Max: 60 * time.Second, Jitter: -0.5}
	if got := b1.NextDelay(0); got != time.Second {
		t.Errorf("negative jitter NextDelay(0) = %s, want 1s", got)
	}
	// Jitter > 1 clamps to 1; just verify the call does not panic and stays
	// within [0, Max].
	b2 := Backoff{Base: time.Second, Max: 60 * time.Second, Jitter: 5}
	for i := 0; i < 50; i++ {
		d := b2.NextDelay(0)
		if d < 0 || d > 60*time.Second {
			t.Errorf("sample %d: %s out of [0, 60s]", i, d)
		}
	}
}

func TestDefaultBackoff(t *testing.T) {
	b := DefaultBackoff()
	if b.Base != time.Second {
		t.Errorf("DefaultBackoff().Base = %s, want 1s", b.Base)
	}
	if b.Max != 60*time.Second {
		t.Errorf("DefaultBackoff().Max = %s, want 60s", b.Max)
	}
	if b.Jitter != 0.1 {
		t.Errorf("DefaultBackoff().Jitter = %v, want 0.1", b.Jitter)
	}
}
