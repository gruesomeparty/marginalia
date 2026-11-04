"""Module doc."""
import os
from x import y

# Retries.
RETRIES = 3

@dataclass
class Batch(Base):
    # Batches.
    """Doc."""
    cap: int = 0
    """Cap doc."""

    def full(self) -> bool:
        return self.cap > 0

    @property
    def size(self):
        pass

async def ingest(b):
    def inner():
        pass
    return b

if __name__ == "__main__":
    ingest(1)
