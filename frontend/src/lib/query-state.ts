interface Read {
  data: unknown;
  error: Error | null;
}

// Only a read with nothing loaded blocks the page: a failed background
// refetch keeps what loaded on screen.
export function blockingError(...reads: readonly Read[]): Error | null {
  return (
    reads.find((r) => r.data === undefined && r.error !== null)?.error ?? null
  );
}
