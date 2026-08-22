import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();
vi.mock("../dom", () => ({
  fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init),
}));

import { MapPaneController } from "./pane";
import type { PlayerPosition, SeamTarget, SlimRoom } from "./types";

function jsonRes(body: unknown): Response {
  return {
    ok: true,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

function stubCanvas(): void {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
    () =>
      new Proxy(
        { fillStyle: "", strokeStyle: "", lineWidth: 0, font: "", textAlign: "", textBaseline: "" },
        { get: (t, p) => (p in t ? (t as never)[p] : () => {}) },
      ) as unknown as CanvasRenderingContext2D,
  );
}

function room(overrides: Partial<SlimRoom>): SlimRoom {
  return {
    z: "A", t: 1, h: "room", x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [],
    p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [],
    ...overrides,
  };
}

function normalizedSeam(tz: string, c: string, tt: number, attrs: Partial<SlimRoom["se"][number]> = {}) {
  return { o: 0, k: "seam", r: "parsed", p: true, tz, tt, c, ...attrs };
}

const playerInA: PlayerPosition = {
  valid: true, confidence: "green", zone: "A", x: 0, y: 0, l: 0, pendingMoves: 0,
};

type PaneInternals = {
  currentZone: string;
  zoneNames: string[];
  statusText: HTMLElement;
  renderer: {
    zoneName: string;
    level: number;
    view: { s: number; ox: number; oy: number };
    findRoom(x: number, y: number, l: number): SlimRoom | undefined;
    centerOn(x: number, y: number, l: number): void;
  };
  showZone(zone: string, opts: { resetView: boolean }): Promise<void>;
  reload(opts?: { preserveView?: boolean; applyPosition?: boolean }): Promise<void>;
  writeEdgeAttrs(side: unknown, patch: { weight?: number }): Promise<void>;
  openEdgeMenu(cell: { z: string; x: number; y: number; l: number }, dir: string, clientX: number, clientY: number): void;
  openSeamMenu(room: SlimRoom, seam: SeamTarget, clientX: number, clientY: number): void;
  zoneCache: Map<string, SlimRoom[]>;
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("map pane corpus refresh", () => {
  let pane: MapPaneController;
  let zones: unknown[];
  let bRooms: SlimRoom[];

  beforeEach(() => {
    stubCanvas();
    zones = [{ zone: "A", room_count: 1 }, { zone: "B", room_count: 2 }];
    bRooms = [
      room({ z: "B", h: "before refresh", x: 4, y: 2, l: 0 }),
      room({ z: "B", h: "upper floor", x: 5, y: 2, l: 1 }),
    ];
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes(zones));
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([room({ z: "A", h: "player room" })]));
      if (url.includes("zone=B")) return Promise.resolve(jsonRes(bRooms));
      if (url.includes("zone=C")) return Promise.resolve(jsonRes([room({ z: "C", h: "fallback room" })]));
      throw new Error(`unexpected fetch ${url}`);
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => 1 });
    document.body.appendChild(pane.root);
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    vi.restoreAllMocks();
  });

  async function viewB(): Promise<PaneInternals> {
    await vi.waitFor(() => expect((pane as unknown as PaneInternals).currentZone).toBe("A"));
    await pane.onRoomPosition(playerInA);
    const internal = pane as unknown as PaneInternals;
    await internal.showZone("B", { resetView: true });
    internal.renderer.level = 1;
    internal.renderer.view = { s: 1.37, ox: 213, oy: -89 };
    return internal;
  }

  it("aborts an older position zone load and applies only the latest position", async () => {
    await vi.waitFor(() => expect((pane as unknown as PaneInternals).currentZone).toBe("A"));
    const firstB = deferred<Response>();
    const secondB = deferred<Response>();
    let bCalls = 0;
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes(zones));
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([room({ z: "A" })]));
      if (url.includes("zone=B")) return ++bCalls === 1 ? firstB.promise : secondB.promise;
      throw new Error(`unexpected fetch ${url}`);
    });

    const old = pane.onRoomPosition({ ...playerInA, zone: "B", x: 1 });
    await vi.waitFor(() => expect(bCalls).toBe(1));
    const latest = pane.onRoomPosition({ ...playerInA, zone: "B", x: 2 });
    await vi.waitFor(() => expect(bCalls).toBe(2));

    firstB.resolve(jsonRes([room({ z: "B", h: "stale position", x: 1 })]));
    await Promise.resolve();
    expect((pane as unknown as PaneInternals).currentZone).toBe("A");
    secondB.resolve(jsonRes([room({ z: "B", h: "latest position", x: 2 })]));
    await Promise.all([old, latest]);
    const internal = pane as unknown as PaneInternals;
    expect(internal.currentZone).toBe("B");
    expect(internal.renderer.findRoom(2, 0, 0)?.h).toBe("latest position");
  });

  it.each([true, false])("keeps an admin view in B during rooms_changed with Follow=%s", async (follow) => {
    const internal = await viewB();
    const followToggle = pane.root.querySelector<HTMLInputElement>(".map-pane__follow input")!;
    followToggle.checked = follow;
    const transform = { ...internal.renderer.view };
    bRooms = [
      room({ z: "B", h: "updated after edit", x: 4, y: 2, l: 0 }),
      room({ z: "B", h: "upper floor", x: 5, y: 2, l: 1 }),
    ];

    await pane.refreshRooms();

    expect(internal.currentZone).toBe("B");
    expect(internal.renderer.zoneName).toBe("B");
    expect(internal.renderer.level).toBe(1);
    expect(internal.renderer.view).toEqual(transform);
    expect(internal.renderer.findRoom(4, 2, 0)?.h).toBe("updated after edit");
  });

  it("keeps following a newly recorded lower floor through rooms_changed", async () => {
    await vi.waitFor(() => expect((pane as unknown as PaneInternals).currentZone).toBe("A"));
    await pane.onRoomPosition(playerInA);

    // room_position is intentionally published before rooms_changed. At this
    // instant the browser's old room corpus does not know that level -1 exists.
    await pane.onRoomPosition({ ...playerInA, l: -1 });
    const lower = room({ z: "A", h: "new lower room", l: -1 });
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes([{ zone: "A", room_count: 2 }]));
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([room({ z: "A", h: "player room" }), lower]));
      throw new Error(`unexpected fetch ${url}`);
    });

    await pane.refreshRooms();

    const internal = pane as unknown as PaneInternals;
    expect(internal.renderer.findRoom(0, 0, -1)?.h).toBe("new lower room");
    expect(internal.renderer.level).toBe(-1);
    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__floors button.on")?.textContent).toBe("-1");
  });

  it("falls back to the player zone when the current zone disappears", async () => {
    const internal = await viewB();
    zones = [{ zone: "A", room_count: 1 }];

    await pane.refreshRooms();

    expect(internal.currentZone).toBe("A");
    expect(internal.renderer.zoneName).toBe("A");
  });

  it("falls back to the first available zone when neither current nor player zone exists", async () => {
    const internal = await viewB();
    zones = [{ zone: "C", room_count: 1 }];

    await pane.refreshRooms();

    expect(internal.currentZone).toBe("C");
    expect(internal.renderer.zoneName).toBe("C");
  });

  it("active corpus reload still resets to the player zone", async () => {
    const internal = await viewB();

    await pane.reload();

    expect(internal.currentZone).toBe("A");
    expect(internal.renderer.zoneName).toBe("A");
  });

  it("keeps a remote camera when an edge write forks the active corpus", async () => {
    let activeID = 1;
    pane.dispose();
    pane.root.remove();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("map-edge-attrs")) {
        activeID = 2; // successful COW write changes the session's active corpus
        return Promise.resolve(jsonRes({ ok: true, forked_to: 2 }));
      }
      if (url.includes("/zones")) return Promise.resolve(jsonRes(zones));
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([room({ z: "A", h: "player room" })]));
      if (url.includes("zone=B")) return Promise.resolve(jsonRes([
        room({ z: "B", h: "forked B", x: 4, y: 2, l: 0 }),
        room({ z: "B", h: "upper floor", x: 5, y: 2, l: 1 }),
      ]));
      throw new Error(`unexpected fetch ${url}`);
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => activeID });
    document.body.appendChild(pane.root);
    const internal = await viewB();
    internal.renderer.level = 1;
    internal.renderer.view = { s: 1.37, ox: 213, oy: -89 };
    const transform = { ...internal.renderer.view };

    await internal.writeEdgeAttrs({
      from: { z: "B", x: 4, y: 2, l: 0 }, to: { z: "B", x: 4, y: 3, l: 0 }, dir: "E",
      fromLabel: "before", toLabel: "after", attrsEditable: true,
    }, { weight: 0.5 });
    // map_sets_changed follows the COW response. It must load set 2 without
    // treating this editor-driven corpus switch as a player-follow request.
    await internal.reload({ preserveView: true, applyPosition: false });

    expect(internal.currentZone).toBe("B");
    expect(internal.renderer.zoneName).toBe("B");
    expect(internal.renderer.level).toBe(1);
    expect(internal.renderer.view).toEqual(transform);
    expect(internal.renderer.findRoom(4, 2, 0)?.h).toBe("forked B");
  });

  it("closes an open seam editor when a refresh removes that seam", async () => {
    bRooms = [
      room({ z: "B", h: "before refresh", x: 4, y: 2, l: 0, se: [normalizedSeam("Elsewhere", "go", 12)] }),
      room({ z: "B", h: "upper floor", x: 5, y: 2, l: 1 }),
    ];
    const internal = await viewB();
    const transform = { ...internal.renderer.view };
    const source = internal.zoneCache.get("B")![0]!;
    internal.openSeamMenu(source, { zone: "Elsewhere", command: "go", tag: 12, source: "parsed", passable: true, order: 0 }, 100, 100);
    expect(pane.root.querySelector(".map-edge-menu")).not.toBeNull();

    bRooms = [
      room({ z: "B", h: "after refresh", x: 4, y: 2, l: 0 }),
      room({ z: "B", h: "upper floor", x: 5, y: 2, l: 1 }),
    ];
    await pane.refreshRooms();

    expect(pane.root.querySelector(".map-edge-menu")).toBeNull();
    expect(internal.renderer.view).toEqual(transform);
  });

  it("refreshes an open seam editor from the latest slim attrs", async () => {
    const seam = { zone: "Elsewhere", command: "go", tag: 12, source: "parsed", passable: true, order: 0 };
    bRooms = [
      room({ z: "B", h: "source", x: 4, y: 2, l: 0, se: [normalizedSeam("Elsewhere", "go", 12)] }),
    ];
    const internal = await viewB();
    internal.openSeamMenu(internal.zoneCache.get("B")![0]!, seam, 100, 100);

    bRooms = [room({
      z: "B", h: "source", x: 4, y: 2, l: 0, se: [normalizedSeam("Elsewhere", "go", 12, { door: true, locked: true, open_cmd: "open portal" })],
    })];
    await pane.refreshRooms();

    const menu = pane.root.querySelector<HTMLElement>(".map-edge-menu")!;
    const checks = [...menu.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')];
    expect(checks.find((input) => input.parentElement?.textContent?.includes("door"))?.checked).toBe(true);
    expect(checks.find((input) => input.parentElement?.textContent?.includes("locked"))?.checked).toBe(true);
    expect([...menu.querySelectorAll<HTMLInputElement>('input[type="text"]')]
      .find((input) => input.parentElement?.textContent?.includes("opening command"))?.value).toBe("open portal");
  });

  it("rolls a pending seam failure back to attrs received during refresh", async () => {
    const seam = { zone: "Elsewhere", command: "go", tag: 12, source: "parsed", passable: true, order: 0 };
    const write = deferred<Response>();
    bRooms = [room({
      z: "B", h: "source", x: 4, y: 2, l: 0, se: [normalizedSeam("Elsewhere", "go", 12, { phrase: "old" })],
    })];
    const internal = await viewB();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-edge-attrs")) return write.promise;
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes(zones));
      if (url.includes("zone=B")) return Promise.resolve(jsonRes(bRooms));
      return Promise.resolve(jsonRes([room({ z: "A" })]));
    });
    internal.openSeamMenu(internal.zoneCache.get("B")![0]!, seam, 100, 100);
    const door = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("door"))!;
    door.click();
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => url.includes("map-edge-attrs"))).toBe(true));

    bRooms = [room({
      z: "B", h: "source", x: 4, y: 2, l: 0, se: [normalizedSeam("Elsewhere", "go", 12, { phrase: "remote" })],
    })];
    await pane.refreshRooms();
    write.resolve(jsonRes({ ok: false, reason: "denied" }));

    await vi.waitFor(() => {
      const phrase = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="text"]')]
        .find((input) => input.parentElement?.textContent?.includes("traversal phrase"));
      expect(phrase?.value).toBe("remote");
    });
  });

  it("refreshes the shared grid attrs editor from the latest slim attrs", async () => {
    bRooms = [
      room({ z: "B", h: "west", x: 4, y: 2, l: 0, g: ["E"] }),
      room({ z: "B", h: "east", x: 4, y: 3, l: 0, g: ["W"] }),
    ];
    const internal = await viewB();
    internal.openEdgeMenu({ z: "B", x: 4, y: 2, l: 0 }, "E", 100, 100);

    bRooms = [
      room({
        z: "B", h: "west", x: 4, y: 2, l: 0, g: ["E"],
        ea: [{ dir: "E", door: true, locked: true, open_cmd: "open east" }],
      }),
      room({ z: "B", h: "east", x: 4, y: 3, l: 0, g: ["W"] }),
    ];
    await pane.refreshRooms();

    const first = pane.root.querySelector<HTMLElement>(".map-edge-menu__side")!;
    expect([...first.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("door"))?.checked).toBe(true);
    expect([...first.querySelectorAll<HTMLInputElement>('input[type="text"]')]
      .find((input) => input.parentElement?.textContent?.includes("opening command"))?.value).toBe("open east");
  });

  it("closes the grid attrs editor when refresh removes its directed passage", async () => {
    bRooms = [
      room({ z: "B", h: "west", x: 4, y: 2, l: 0, g: ["E"] }),
      room({ z: "B", h: "east", x: 4, y: 3, l: 0, g: ["W"] }),
    ];
    const internal = await viewB();
    internal.openEdgeMenu({ z: "B", x: 4, y: 2, l: 0 }, "E", 100, 100);
    expect(pane.root.querySelector(".map-edge-menu__attrs")).not.toBeNull();

    bRooms = [
      room({ z: "B", h: "west", x: 4, y: 2, l: 0 }),
      room({ z: "B", h: "east", x: 4, y: 3, l: 0 }),
    ];
    await pane.refreshRooms();

    expect(pane.root.querySelector(".map-edge-menu")).toBeNull();
  });

  it("disables every grid popup control during an attrs write but still lets Escape close it", async () => {
    const write = deferred<Response>();
    bRooms = [
      room({ z: "B", h: "west", x: 4, y: 2, l: 0, g: ["E"] }),
      room({ z: "B", h: "east", x: 4, y: 3, l: 0, g: ["W"] }),
    ];
    const internal = await viewB();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-edge-attrs")) return write.promise;
      return Promise.resolve(jsonRes({ seen: false }));
    });
    internal.openEdgeMenu({ z: "B", x: 4, y: 2, l: 0 }, "E", 100, 100);
    await new Promise((resolve) => setTimeout(resolve, 0));

    const menu = pane.root.querySelector<HTMLElement>(".map-edge-menu")!;
    [...menu.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("door"))!
      .click();
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => url.includes("map-edge-attrs"))).toBe(true));

    const controls = [...menu.querySelectorAll<HTMLInputElement | HTMLButtonElement>("input, button")];
    expect(controls.length).toBeGreaterThan(4);
    expect(controls.every((control) => control.disabled)).toBe(true);

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(pane.root.querySelector(".map-edge-menu")).toBeNull();
    write.resolve(jsonRes({ ok: true }));
  });

  it("does not apply Follow after a preserve-view corpus reload", async () => {
    const internal = await viewB();
    const followToggle = pane.root.querySelector<HTMLInputElement>(".map-pane__follow input")!;
    followToggle.checked = true;
    const transform = { ...internal.renderer.view };

    await pane.reload({ preserveView: true });

    expect(internal.currentZone).toBe("B");
    expect(internal.renderer.zoneName).toBe("B");
    expect(internal.renderer.level).toBe(1);
    expect(internal.renderer.view).toEqual(transform);
  });

  it("does not let the COW position replay overtake its preserve-view reload", async () => {
    const internal = await viewB();
    const followToggle = pane.root.querySelector<HTMLInputElement>(".map-pane__follow input")!;
    followToggle.checked = true;
    const transform = { ...internal.renderer.view };
    // WriteTopology emits the unchanged tracker position after map_sets_changed,
    // while the active-set REST refresh may still be in flight.
    await pane.onRoomPosition(playerInA);
    await pane.reload({ preserveView: true });

    expect(internal.currentZone).toBe("B");
    expect(internal.renderer.zoneName).toBe("B");
    expect(internal.renderer.level).toBe(1);
    expect(internal.renderer.view).toEqual(transform);
  });

  it("keeps the remote view across the position replay of a no-fork edge write", async () => {
    const internal = await viewB();
    const followToggle = pane.root.querySelector<HTMLInputElement>(".map-pane__follow input")!;
    followToggle.checked = true;
    const edgeWrite = deferred<Response>();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-edge-attrs")) return edgeWrite.promise;
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes(zones));
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([room({ z: "A" })]));
      if (url.includes("zone=B")) return Promise.resolve(jsonRes(bRooms));
      throw new Error(`unexpected fetch ${url}`);
    });

    const write = internal.writeEdgeAttrs({
      from: { z: "B", x: 4, y: 2, l: 0 }, to: { z: "B", x: 4, y: 3, l: 0 }, dir: "E",
      fromLabel: "before", toLabel: "after", attrsEditable: true,
    }, { weight: 0.5 });
    await Promise.resolve();
    await pane.onRoomPosition(playerInA);
    edgeWrite.resolve(jsonRes({ ok: true }));
    await write;
    await pane.refreshRooms();

    expect(internal.currentZone).toBe("B");
    expect(internal.renderer.zoneName).toBe("B");
  });

  it("does not inhibit Follow after an edge write soft-fails", async () => {
    const internal = await viewB();
    const followToggle = pane.root.querySelector<HTMLInputElement>(".map-pane__follow input")!;
    followToggle.checked = true;
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-edge-attrs")) return Promise.resolve(jsonRes({ ok: false, reason: "missing face" }));
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes(zones));
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([room({ z: "A" })]));
      if (url.includes("zone=B")) return Promise.resolve(jsonRes(bRooms));
      throw new Error(`unexpected fetch ${url}`);
    });

    await internal.writeEdgeAttrs({
      from: { z: "B", x: 4, y: 2, l: 0 }, to: { z: "B", x: 4, y: 3, l: 0 }, dir: "E",
      fromLabel: "before", toLabel: "after", attrsEditable: true,
    }, { weight: 0.5 });
    await pane.onRoomPosition({ ...playerInA, x: 1 });

    expect(internal.currentZone).toBe("A");
    expect(internal.renderer.zoneName).toBe("A");
  });

  it("lets an explicit C selection win over a delayed rooms_changed refresh for B", async () => {
    const internal = await viewB();
    const delayedB = deferred<Response>();
    let delayB = true;
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(jsonRes([{ zone: "A", room_count: 1 }, { zone: "B", room_count: 1 }, { zone: "C", room_count: 1 }]));
      if (url.includes("zone=B") && delayB) return delayedB.promise;
      if (url.includes("zone=B")) return Promise.resolve(jsonRes(bRooms));
      if (url.includes("zone=C")) return Promise.resolve(jsonRes([room({ z: "C", h: "explicit C" })]));
      return Promise.resolve(jsonRes([room({ z: "A" })]));
    });

    const refresh = pane.refreshRooms();
    await Promise.resolve();
    await Promise.resolve();
    await internal.showZone("C", { resetView: true });
    delayB = false;
    delayedB.resolve(jsonRes([room({ z: "B", h: "stale B" })]));
    await refresh;

    expect(internal.currentZone).toBe("C");
    expect(internal.renderer.zoneName).toBe("C");
  });

  it("does not cache an old corpus response after a newer corpus reload", async () => {
    let activeID = 1;
    const oldRooms = deferred<Response>();
    let delayOld = true;
    pane.dispose();
    pane.root.remove();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("map-sets/1/zones")) return Promise.resolve(jsonRes([{ zone: "A", room_count: 1 }]));
      if (url.includes("map-sets/2/zones")) return Promise.resolve(jsonRes([{ zone: "C", room_count: 1 }]));
      if (url.includes("map_set=1") && delayOld) return oldRooms.promise;
      if (url.includes("map_set=2")) return Promise.resolve(jsonRes([room({ z: "C", h: "corpus two" })]));
      return Promise.resolve(jsonRes([]));
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => activeID });
    document.body.appendChild(pane.root);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("map_set=1"), undefined));
    const oldReload = pane.reload();
    activeID = 2;
    const newReload = pane.reload();
    delayOld = false;
    oldRooms.resolve(jsonRes([room({ z: "A", h: "stale corpus one" })]));
    await Promise.all([oldReload, newReload]);

    const internal = pane as unknown as PaneInternals;
    expect(internal.currentZone).toBe("C");
    expect(internal.renderer.zoneName).toBe("C");
    expect(internal.zoneCache.has("A")).toBe(false);
  });

  it("ignores a late zone response after dispose", async () => {
    await vi.waitFor(() => expect((pane as unknown as PaneInternals).currentZone).toBe("A"));
    const delayedZones = deferred<Response>();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(jsonRes({ seen: false }));
      if (url.includes("/zones")) return delayedZones.promise;
      return Promise.resolve(jsonRes([]));
    });
    const reload = pane.reload();
    pane.dispose();
    delayedZones.resolve(jsonRes([{ zone: "C", room_count: 1 }]));
    await reload;

    expect((pane as unknown as PaneInternals).currentZone).toBe("A");
  });

  it("does not let a delayed seam jump overwrite a later explicit zone selection", async () => {
    const internal = await viewB();
    internal.zoneNames = ["A", "B", "C"];
    const delayedC = deferred<Response>();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("zone=C")) return delayedC.promise;
      if (url.includes("zone=B")) return Promise.resolve(jsonRes(bRooms));
      return Promise.resolve(jsonRes([]));
    });
    const center = vi.spyOn(internal.renderer, "centerOn");
    const jump = (pane as unknown as { jumpSeam: (seam: SeamTarget, room: SlimRoom) => Promise<void> })
      .jumpSeam({ zone: "legacy-wrong", command: "go", tag: 1, source: "parsed", passable: true, order: 0,
        to: { zone: "C", x: 0, y: 0, l: 0 } }, bRooms[0]);
    await Promise.resolve();
    await internal.showZone("B", { resetView: true });
    delayedC.resolve(jsonRes([room({ z: "C", t: 1 })]));
    await jump;

    expect(internal.currentZone).toBe("B");
    expect(center).not.toHaveBeenCalled();
    expect(internal.statusText.textContent).not.toContain("Jumped");
  });

  it("does not center or publish a stale success after a seam fetch failure", async () => {
    const internal = await viewB();
    internal.zoneNames = ["A", "B", "C"];
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("zone=C")) return Promise.resolve({ ok: false, text: async () => "gone" } as Response);
      return Promise.resolve(jsonRes(bRooms));
    });
    const center = vi.spyOn(internal.renderer, "centerOn");
    await (pane as unknown as { jumpSeam: (seam: SeamTarget, room: SlimRoom) => Promise<void> })
      .jumpSeam({ zone: "C", command: "go", tag: 1, source: "parsed", passable: true, order: 0,
        to: { zone: "C", x: 0, y: 0, l: 0 } }, bRooms[0]);

    expect(center).not.toHaveBeenCalled();
    expect(internal.statusText.textContent).not.toContain("Jumped");
  });

  it.each([
    ["null", false], ["null", true], ["empty", false], ["empty", true],
  ])("clears old rooms when the next corpus is %s (preserve=%s)", async (kind, preserve) => {
    await viewB();
    const controller = pane as unknown as { config: { getActiveMapSetID: () => number | null } };
    controller.config.getActiveMapSetID = () => kind === "null" ? null : 1;
    zones = kind === "empty" ? [] : zones;
    await pane.reload(preserve ? { preserveView: true } : undefined);

    expect((pane as unknown as PaneInternals).renderer.zoneName).toBe("");
    expect((pane as unknown as PaneInternals).renderer.findRoom(4, 2, 0)).toBeUndefined();
  });
});
