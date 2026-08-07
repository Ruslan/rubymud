import { describe, expect, it } from "vitest";

import { LatestRequest } from "./latestRequest";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("LatestRequest", () => {
  it("accepts only the newest active-map-set response when replies reverse", async () => {
    const gate = new LatestRequest();
    const first = deferred<number | null>();
    const second = deferred<number | null>();
    let active: number | null = null;
    const apply = async (request: number, reply: Promise<number | null>) => {
      const value = await reply;
      if (gate.isCurrent(request)) active = value;
    };

    const oldRequest = gate.begin();
    const old = apply(oldRequest, first.promise);
    const newRequest = gate.begin();
    const newer = apply(newRequest, second.promise);
    second.resolve(2);
    await newer;
    first.resolve(1);
    await old;

    expect(active).toBe(2);
  });
});
