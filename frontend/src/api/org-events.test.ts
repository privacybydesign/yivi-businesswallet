import { describe, expect, it } from "vitest";
import { followOrgEvents } from "./org-events";
import type { OrgSignal, OrgStream } from "./org-events";

// A stream the test drives: emit fires its listeners, closed says whether the
// follower closed it.
class FakeStream implements OrgStream {
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

describe("followOrgEvents", () => {
  it("names what changed", async () => {
    const stream = new FakeStream();
    const signals: OrgSignal[] = [];
    const stop = followOrgEvents(
      "kinds",
      () => stream,
      (what) => signals.push(what),
    );
    await settle();
    stream.emit("qerds");
    stream.emit("signing");
    await settle();
    expect(signals).toEqual(["qerds", "signing"]);
    stop();
  });

  it("opens one stream for every tab and passes each change on", async () => {
    const streams: FakeStream[] = [];
    const open = (): FakeStream => {
      const stream = new FakeStream();
      streams.push(stream);
      return stream;
    };
    const first: OrgSignal[] = [];
    const second: OrgSignal[] = [];
    const stopFirst = followOrgEvents("acme", open, (what) => first.push(what));
    const stopSecond = followOrgEvents("acme", open, (what) =>
      second.push(what),
    );
    await settle();
    expect(streams).toHaveLength(1);

    streams[0].emit("reviews");
    await settle();
    expect([first, second]).toEqual([["reviews"], ["reviews"]]);

    // The leader stops: the other takes over with a stream of its own.
    stopFirst();
    await settle();
    expect(streams[0].closed).toBe(true);
    expect(streams).toHaveLength(2);
    streams[1].emit("open");
    await settle();
    expect(second).toEqual(["reviews", "connected"]);

    stopSecond();
    await settle();
    expect(streams[1].closed).toBe(true);
  });

  it("says when the stream drops, so its reads can poll meanwhile", async () => {
    const streams: FakeStream[] = [];
    const signals: OrgSignal[] = [];
    const stop = followOrgEvents(
      "drops",
      () => {
        const stream = new FakeStream();
        streams.push(stream);
        return stream;
      },
      (what) => signals.push(what),
    );
    await settle();
    streams[0].emit("open");
    streams[0].emit("error");
    await settle();
    expect(signals).toEqual(["connected", "disconnected"]);
    expect(streams[0].closed).toBe(true);
    stop();
  });

  it("opens a new stream when the one it has goes silent", async () => {
    const streams: FakeStream[] = [];
    const signals: OrgSignal[] = [];
    const stop = followOrgEvents(
      "silent",
      () => {
        const stream = new FakeStream();
        streams.push(stream);
        return stream;
      },
      (what) => signals.push(what),
      60,
    );
    await settle();
    streams[0].emit("open");
    // A ping in time keeps it.
    await new Promise((resolve) => setTimeout(resolve, 40));
    streams[0].emit("ping");
    await new Promise((resolve) => setTimeout(resolve, 40));
    expect(streams).toHaveLength(1);
    // Silence past the limit: dropped and opened again, without an error.
    await new Promise((resolve) => setTimeout(resolve, 80));
    expect(streams[0].closed).toBe(true);
    expect(streams.length).toBeGreaterThan(1);
    expect(signals.slice(0, 2)).toEqual(["connected", "disconnected"]);
    stop();
  });

  // React's StrictMode, and a fast navigation, start following, stop and
  // start again within one tick. The lock can be granted to the stopped one
  // just then; it must not lead, or the live follower never gets the stream.
  it("lets a follower lead when the one before it stopped at once", async () => {
    const streams: FakeStream[] = [];
    const open = (): FakeStream => {
      const stream = new FakeStream();
      streams.push(stream);
      return stream;
    };
    const signals: OrgSignal[] = [];
    for (let i = 0; i < 20; i++) {
      followOrgEvents("remount", open, () => undefined)();
    }
    const stop = followOrgEvents("remount", open, (what) => signals.push(what));
    await settle();
    const live = streams.filter((stream) => !stream.closed);
    expect(live).toHaveLength(1);
    live[0].emit("open");
    live[0].emit("reviews");
    await settle();
    expect(signals).toEqual(["connected", "reviews"]);
    stop();
  });

  // A tab the browser froze keeps the lock but says nothing; the other tabs
  // must not wait on it forever.
  it("takes the stream over from a leader that went silent", async () => {
    const streams: FakeStream[] = [];
    const signals: OrgSignal[] = [];
    let release = (): void => undefined;
    void navigator.locks
      .request(
        "org-events:frozen",
        () =>
          new Promise<void>((resolve) => {
            release = resolve;
          }),
      )
      .catch(() => undefined);
    await settle();
    const stop = followOrgEvents(
      "frozen",
      () => {
        const stream = new FakeStream();
        streams.push(stream);
        return stream;
      },
      (what) => signals.push(what),
      30,
    );
    await settle();
    expect(streams).toHaveLength(0);
    // Past twice the stale limit without a word from the leader.
    await new Promise((resolve) => setTimeout(resolve, 80));
    const live = streams.filter((stream) => !stream.closed);
    expect(live).toHaveLength(1);
    live[0].emit("open");
    live[0].emit("attestations");
    await settle();
    expect(signals.slice(-2)).toEqual(["connected", "attestations"]);
    stop();
    release();
  });

  it("closes its stream when another tab takes the lock over", async () => {
    const streams: FakeStream[] = [];
    const stop = followOrgEvents(
      "stolen",
      () => {
        const stream = new FakeStream();
        streams.push(stream);
        return stream;
      },
      () => undefined,
    );
    await settle();
    expect(streams).toHaveLength(1);
    let release = (): void => undefined;
    void navigator.locks.request(
      "org-events:stolen",
      { steal: true },
      () =>
        new Promise<void>((resolve) => {
          release = resolve;
        }),
    );
    await settle();
    expect(streams[0].closed).toBe(true);
    stop();
    release();
  });

  it("gives another org a stream of its own", async () => {
    let opened = 0;
    const open = (): FakeStream => {
      opened++;
      return new FakeStream();
    };
    const stops = [
      followOrgEvents("acme", open, () => undefined),
      followOrgEvents("beta", open, () => undefined),
    ];
    await settle();
    expect(opened).toBe(2);
    for (const stop of stops) stop();
  });
});
