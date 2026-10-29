import { describe, expect, it } from "vitest";
import { followReviewEvents } from "./review-events";
import type { ReviewStream } from "./review-events";

// A stream the test drives: emit fires its listeners, closed says whether the
// follower closed it.
class FakeStream implements ReviewStream {
  closed = false;
  private readonly listeners = new Map<string, (() => void)[]>();

  addEventListener(type: string, listener: () => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener();
  }
}

// Lets the lock and channel deliver what is queued.
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 20));
}

describe("followReviewEvents", () => {
  it("opens one stream for every tab and passes each change on", async () => {
    const streams: FakeStream[] = [];
    const open = (): FakeStream => {
      const stream = new FakeStream();
      streams.push(stream);
      return stream;
    };
    let first = 0;
    let second = 0;
    const stopFirst = followReviewEvents("acme", open, () => first++);
    const stopSecond = followReviewEvents("acme", open, () => second++);
    await settle();
    expect(streams).toHaveLength(1);

    streams[0].emit("reviews");
    await settle();
    expect([first, second]).toEqual([1, 1]);

    // The leader stops: the other takes over with a stream of its own.
    stopFirst();
    await settle();
    expect(streams[0].closed).toBe(true);
    expect(streams).toHaveLength(2);
    streams[1].emit("open");
    await settle();
    expect(second).toBe(2);

    stopSecond();
    await settle();
    expect(streams[1].closed).toBe(true);
  });

  it("gives another org a stream of its own", async () => {
    let opened = 0;
    const open = (): FakeStream => {
      opened++;
      return new FakeStream();
    };
    const stops = [
      followReviewEvents("acme", open, () => undefined),
      followReviewEvents("beta", open, () => undefined),
    ];
    await settle();
    expect(opened).toBe(2);
    for (const stop of stops) stop();
  });
});
