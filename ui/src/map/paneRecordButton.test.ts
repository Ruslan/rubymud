import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The pane fetches through ../dom; give it a controllable stub so no test ever
// touches the network. Declared before the import of pane.ts (vi.mock is hoisted).
const fetchMock = vi.fn();
vi.mock("../dom", () => ({
  fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init),
}));

import { MapPaneController } from "./pane";
import type { LiveRoom, PlayerPosition } from "./types";

// DOM-level coverage of the top-bar «Записать комнату» button. recordMenu.test.ts
// pins the pure gate; this pins that the pane actually MOUNTS that button, keeps
// it visible, reflects the gate in disabled/title, and — the whole point of the
// button — POSTs the same map-record-room the cell menu does, without the
// operator having to find and right-click the right cell.

function jsonRes(body: unknown): Response {
  return {
    ok: true,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

// jsdom's canvas has no 2d context and MapRenderer throws without one.
function stubCanvas(): void {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
    () =>
      new Proxy(
        { fillStyle: "", strokeStyle: "", lineWidth: 0, font: "", textAlign: "", textBaseline: "" },
        { get: (t, p) => (p in t ? (t as never)[p] : () => {}) },
      ) as unknown as CanvasRenderingContext2D,
  );
}

function pos(over: Partial<PlayerPosition> = {}): PlayerPosition {
  return {
    valid: true,
    zone: "Z",
    x: 1,
    y: 1,
    l: 0,
    confidence: "yellow",
    pendingMoves: 0,
    eventDescribesCell: true,
    ...over,
  };
}

const live: LiveRoom = { hint: "Новая", desc: "prose", exits: "W E" };

function recordBtn(pane: MapPaneController): HTMLButtonElement {
  const btn = pane.root.querySelector<HTMLButtonElement>(".map-pane__record");
  if (!btn) throw new Error("record button not mounted");
  return btn;
}

describe("map pane record button", () => {
  let pane: MapPaneController;

  beforeEach(() => {
    stubCanvas();
    fetchMock.mockReset();
    // No active map set: loadMapSet short-circuits, so the constructor makes no
    // zone requests and the button's state comes purely from what we push in.
    fetchMock.mockResolvedValue(jsonRes({ seen: false }));
    pane = new MapPaneController({
      sessionID: 7,
      getActiveMapSetID: () => null,
    });
  });

  afterEach(() => {
    pane.dispose();
    vi.restoreAllMocks();
  });

  it("is mounted in the control bar next to the confidence badge", () => {
    const bar = pane.root.querySelector(".map-pane__bar");
    expect(bar?.contains(recordBtn(pane))).toBe(true);
    expect(recordBtn(pane).textContent).toBe("Записать комнату");
  });

  it("starts disabled with a reason, before any position is known", () => {
    const btn = recordBtn(pane);
    expect(btn.disabled).toBe(true);
    expect(btn.title).toContain("нет позиции");
  });

  it("stays VISIBLE while disabled — it must not vanish when unavailable", () => {
    const btn = recordBtn(pane);
    expect(btn.disabled).toBe(true);
    expect(pane.root.contains(btn)).toBe(true);
    expect(btn.hidden).toBe(false);
    expect(btn.style.display).not.toBe("none");
  });

  it("enables once the tracker has a 🟡 position and a live room block", async () => {
    await pane.onRoomPosition(pos());
    pane.onRoomEvent(live);
    const btn = recordBtn(pane);
    expect(btn.disabled).toBe(false);
    expect(btn.title).toContain("Новая");
  });

  it("goes back to disabled, with the reason, when the position is lost", async () => {
    await pane.onRoomPosition(pos());
    pane.onRoomEvent(live);
    expect(recordBtn(pane).disabled).toBe(false);
    await pane.onRoomPosition(pos({ confidence: "red", eventDescribesCell: false }));
    const btn = recordBtn(pane);
    expect(btn.disabled).toBe(true);
    expect(btn.title).toContain("🔴");
  });

  it("POSTs map-record-room for the tracker's own cell when clicked", async () => {
    await pane.onRoomPosition(pos({ zone: "Склад", x: 4, y: 7, l: 2 }));
    pane.onRoomEvent(live);
    fetchMock.mockReset();
    fetchMock.mockResolvedValue(jsonRes({ applied: true, created: true }));

    recordBtn(pane).click();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/sessions/7/map-record-room");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ zone: "Склад", x: 4, y: 7, l: 2 });
  });

  it("does not POST while disabled", async () => {
    await pane.onRoomPosition(pos({ confidence: "red", eventDescribesCell: false }));
    pane.onRoomEvent(live);
    fetchMock.mockReset();
    recordBtn(pane).click();
    await Promise.resolve();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
