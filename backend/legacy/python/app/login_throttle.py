from collections import deque
from math import ceil
from threading import Lock
from time import monotonic

from fastapi import HTTPException


class LoginThrottle:
    """Bound guesses for the single account in the single API process."""

    def __init__(self):
        self.attempts: deque[float] = deque()
        self.lock = Lock()

    def check(self) -> None:
        with self.lock:
            now = monotonic()
            while self.attempts and self.attempts[0] <= now - 60:
                self.attempts.popleft()
            if len(self.attempts) >= 5:
                retry_after = max(1, ceil(self.attempts[0] + 60 - now))
                raise HTTPException(429, "Too many login attempts. Try again shortly.",
                                    headers={"Retry-After": str(retry_after)})
            self.attempts.append(now)

    def clear(self) -> None:
        with self.lock:
            self.attempts.clear()
