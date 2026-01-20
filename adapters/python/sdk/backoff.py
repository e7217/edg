"""Backoff strategy for retry logic"""

from __future__ import annotations

import random


class BackoffStrategy:
    """Exponential backoff strategy with optional jitter

    Calculates delay for retry attempts using exponential backoff:
    delay = min(base * 2^attempt, max_delay)

    With jitter, adds randomness to prevent thundering herd:
    delay = delay * (1 ± jitter)

    Args:
        base: Base delay in seconds (default: 1.0)
        max_delay: Maximum delay in seconds (default: 60.0)
        jitter: Jitter factor (0.0 to 1.0, default: 0.0)

    Example:
        >>> backoff = BackoffStrategy(base=1.0, max_delay=60.0, jitter=0.1)
        >>> backoff.next_delay(attempt=0)  # ~1.0 ± 10%
        >>> backoff.next_delay(attempt=3)  # ~8.0 ± 10%
    """

    def __init__(
        self,
        base: float = 1.0,
        max_delay: float = 60.0,
        jitter: float = 0.0,
    ):
        """Initialize backoff strategy

        Args:
            base: Base delay in seconds
            max_delay: Maximum delay in seconds
            jitter: Jitter factor (0.0 to 1.0) for randomness
        """
        self.base = base
        self.max_delay = max_delay
        self.jitter = jitter

    def next_delay(self, attempt: int) -> float:
        """Calculate next delay for the given attempt number

        Args:
            attempt: Retry attempt number (0-based)

        Returns:
            Delay in seconds with exponential backoff and optional jitter
        """
        # Handle negative attempts
        if attempt < 0:
            attempt = 0

        # Calculate exponential backoff: base * 2^attempt
        delay = self.base * (2**attempt)

        # Clamp to max_delay
        delay = min(delay, self.max_delay)

        # Apply jitter if configured
        if self.jitter > 0:
            # Add random jitter: delay * (1 ± jitter)
            jitter_amount = delay * self.jitter
            delay = delay + random.uniform(-jitter_amount, jitter_amount)

            # Ensure delay doesn't go negative or exceed max_delay
            delay = max(0, min(delay, self.max_delay))

        return delay
