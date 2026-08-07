import { describe, expect, it, vi } from "vitest";
import { ActiveMapSetRefresh, cowNotificationMapSetID } from "./activeMapSetRefresh";

function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }

describe("ActiveMapSetRefresh", () => {
  it("keeps the newest successful response when replies reverse", async () => {
    const first = deferred<number | null>(), second = deferred<number | null>(); let active: number | null = 0, reloads = 0, call = 0;
    const refresh = new ActiveMapSetRefresh(() => (++call === 1 ? first.promise : second.promise), () => active, (v) => { active = v; }, () => { reloads++; });
    const old = refresh.refresh(), newer = refresh.refresh(); second.resolve(2); await newer; first.resolve(1); await old;
    expect(active).toBe(2); expect(reloads).toBe(1);
  });

  it("forces exactly one fallback reload on a current failure but not a stale failure", async () => {
    const first = deferred<number | null>(); let call = 0, reloads = 0;
    const refresh = new ActiveMapSetRefresh(() => (++call === 1 ? first.promise : Promise.reject(new Error("down"))), () => 7, vi.fn(), () => { reloads++; });
    const stale = refresh.refresh(true), current = refresh.refresh(true); await current; first.resolve(1); await stale;
    expect(reloads).toBe(1);
  });

  it("does not reload for a normal startup failure", async () => {
    const reload = vi.fn();
    const refresh = new ActiveMapSetRefresh(() => Promise.reject(new Error("down")), () => 7, vi.fn(), reload);
    await refresh.refresh(false);
    expect(reload).not.toHaveBeenCalled();
  });

  it("uses a normal reset reload when a COW-scoped active read fails", async () => {
    const reload = vi.fn();
    const refresh = new ActiveMapSetRefresh(() => Promise.reject(new Error("down")), () => 1, vi.fn(), reload);
    await refresh.refresh(true, 2);
    expect(reload).toHaveBeenCalledWith(false);
  });

  it("forwards preserve-view only for the caller that requested it", async () => {
    const reload = vi.fn();
    const refresh = new ActiveMapSetRefresh(async () => 2, () => 1, vi.fn(), reload);
    await refresh.refresh(true, 2);
    expect(reload).toHaveBeenCalledWith(true);
  });

  it("does not preserve a COW view across a later external active-set switch", async () => {
    const reload = vi.fn();
    // The COW notification was for set 2, but the authoritative read now says 3.
    const refresh = new ActiveMapSetRefresh(async () => 3, () => 1, vi.fn(), reload);
    await refresh.refresh(true, 2);
    expect(reload).toHaveBeenCalledWith(false);
  });

  it.each([
    [undefined, undefined],
    [0, undefined],
    [-1, undefined],
    [2, 2],
  ])("classifies map_set_id %s as COW target %s", (raw, expected) => {
    expect(cowNotificationMapSetID(raw)).toBe(expected);
  });

  it("does not leak a stale preserve request when REST replies reverse", async () => {
    const first = deferred<number | null>();
    let active: number | null = 1;
    let calls = 0;
    const reload = vi.fn();
    const refresh = new ActiveMapSetRefresh(
      () => ++calls === 1 ? first.promise : Promise.resolve(3),
      () => active,
      (next) => { active = next; },
      reload,
    );
    const staleCOW = refresh.refresh(true, 2);
    await refresh.refresh(true);
    first.resolve(2);
    await staleCOW;
    expect(active).toBe(3);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(reload).toHaveBeenCalledWith(false);
  });
});
