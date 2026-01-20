"""Tests for backoff strategy"""

import pytest

from sdk.backoff import BackoffStrategy


class TestBackoffStrategyBasic:
    """Test basic backoff functionality"""

    def test_exponential_backoff_first_attempt(self):
        """First attempt should return base delay"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0)
        assert backoff.next_delay(attempt=0) == 1.0

    def test_exponential_backoff_second_attempt(self):
        """Second attempt should double the delay"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0)
        assert backoff.next_delay(attempt=1) == 2.0

    def test_exponential_backoff_third_attempt(self):
        """Third attempt should quadruple the delay"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0)
        assert backoff.next_delay(attempt=2) == 4.0

    def test_exponential_backoff_respects_max_delay(self):
        """Delay should not exceed max_delay"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0)
        # Attempt 10 would be 2^10 = 1024 seconds without max
        delay = backoff.next_delay(attempt=10)
        assert delay <= 60.0

    def test_exponential_backoff_formula(self):
        """Test the exponential backoff formula: base * 2^attempt"""
        backoff = BackoffStrategy(base=2.0, max_delay=1000.0)
        assert backoff.next_delay(attempt=0) == 2.0  # 2 * 2^0 = 2
        assert backoff.next_delay(attempt=1) == 4.0  # 2 * 2^1 = 4
        assert backoff.next_delay(attempt=2) == 8.0  # 2 * 2^2 = 8
        assert backoff.next_delay(attempt=3) == 16.0  # 2 * 2^3 = 16


class TestBackoffStrategyWithJitter:
    """Test backoff with jitter (randomness)"""

    def test_backoff_with_jitter_range(self):
        """Jitter should add randomness within specified range"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0, jitter=0.1)
        delay = backoff.next_delay(attempt=0)
        # With 10% jitter, delay should be between 0.9 and 1.1
        assert 0.9 <= delay <= 1.1

    def test_backoff_with_jitter_multiple_attempts(self):
        """Jitter should apply to all attempts"""
        backoff = BackoffStrategy(base=2.0, max_delay=60.0, jitter=0.2)

        # Attempt 0: base is 2.0, with 20% jitter should be 1.6 to 2.4
        delay = backoff.next_delay(attempt=0)
        assert 1.6 <= delay <= 2.4

        # Attempt 1: base is 4.0, with 20% jitter should be 3.2 to 4.8
        delay = backoff.next_delay(attempt=1)
        assert 3.2 <= delay <= 4.8

    def test_backoff_zero_jitter(self):
        """Zero jitter should produce deterministic delays"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0, jitter=0.0)
        # Without jitter, delays should be exactly the base * 2^attempt
        assert backoff.next_delay(attempt=0) == 1.0
        assert backoff.next_delay(attempt=1) == 2.0


class TestBackoffStrategyEdgeCases:
    """Test edge cases and validation"""

    def test_backoff_with_max_delay_smaller_than_base(self):
        """max_delay smaller than base should clamp to max_delay"""
        backoff = BackoffStrategy(base=10.0, max_delay=5.0)
        # Even first attempt should be clamped to max_delay
        delay = backoff.next_delay(attempt=0)
        assert delay <= 5.0

    def test_backoff_negative_attempt(self):
        """Negative attempt should be treated as 0"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0)
        delay = backoff.next_delay(attempt=-1)
        assert delay == 1.0

    def test_backoff_custom_parameters(self):
        """Test with custom base and max_delay values"""
        backoff = BackoffStrategy(base=0.5, max_delay=30.0)
        assert backoff.next_delay(attempt=0) == 0.5
        assert backoff.next_delay(attempt=1) == 1.0
        assert backoff.next_delay(attempt=6) <= 30.0  # 0.5 * 2^6 = 32, should clamp to 30

    def test_backoff_default_parameters(self):
        """Test default parameter values"""
        backoff = BackoffStrategy()
        # Should have reasonable defaults
        delay = backoff.next_delay(attempt=0)
        assert delay > 0
        assert delay < 100  # Reasonable upper bound for default


class TestBackoffStrategyReset:
    """Test reset functionality"""

    def test_reset_backoff(self):
        """Reset should allow backoff to be reused"""
        backoff = BackoffStrategy(base=1.0, max_delay=60.0)

        # Use backoff for a few attempts
        backoff.next_delay(attempt=0)
        backoff.next_delay(attempt=5)

        # Reset should not affect subsequent calls
        # (BackoffStrategy is stateless, so reset is implicit)
        delay = backoff.next_delay(attempt=0)
        assert delay == 1.0
