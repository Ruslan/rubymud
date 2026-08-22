import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();
vi.mock("../dom", () => ({
  fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init),
}));

import { statusAutoHideDelay } from "./cellMenu";
import { MapPaneController } from "./pane";
import type { SlimRoom } from "./types";

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

const room: SlimRoom = {
  z: "Z", t: 1, h: "Длинная дорога", x: 2, y: 3, l: 0,
  e: [], d: [], ch: 0, se: [], p: false, i: null, s: false,
  dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [],
};

interface PaneInternals {
  config: { setInputText?: (text: string) => void };
  setStatus: (text: string, opts?: { sticky?: boolean }) => void;
  routeToCell: (
    cell: { z: string; x: number; y: number; l: number },
    room: SlimRoom,
    action: "insert" | "send",
  ) => Promise<void>;
}

function status(pane: MapPaneController): HTMLElement {
  const el = pane.root.querySelector<HTMLElement>(".map-pane__status");
  if (!el) throw new Error("status not mounted");
  return el;
}

describe("map pane status", () => {
  let pane: MapPaneController;
  let internals: PaneInternals;

  beforeEach(() => {
    vi.useFakeTimers();
    stubCanvas();
    fetchMock.mockReset();
    pane = new MapPaneController({
      sessionID: 7,
      getActiveMapSetID: () => null,
      setInputText: vi.fn(),
    });
    document.body.appendChild(pane.root);
    internals = pane as unknown as PaneInternals;
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("has a keyboard-focusable close button that dismisses even a sticky status", () => {
    internals.setStatus("Карта записана", { sticky: true });
    const close = status(pane).querySelector<HTMLButtonElement>(
      ".map-pane__status-close",
    );
    expect(close?.type).toBe("button");
    expect(close?.getAttribute("aria-label")).toBe("Dismiss message");
    expect(close?.title).toBe("Dismiss message");
    close?.focus();
    expect(document.activeElement).toBe(close);
    close?.click();
    expect(status(pane).style.display).toBe("none");
  });

  it("auto-hides an unflagged successful route inserted into input", async () => {
    fetchMock.mockResolvedValue(
      jsonRes({ reachable: true, directions: ["N", "E"], summary: "2 steps" }),
    );
    await internals.routeToCell(room, room, "insert");
    const text = 'Route to "Длинная дорога": 2 steps → input';
    expect(status(pane).textContent).toContain(text);
    await vi.advanceTimersByTimeAsync(statusAutoHideDelay(text) - 1);
    expect(status(pane).style.display).not.toBe("none");
    await vi.advanceTimersByTimeAsync(1);
    expect(status(pane).style.display).toBe("none");
  });

  it("also auto-hides the successful insert fallback without an input callback", async () => {
    internals.config.setInputText = undefined;
    fetchMock.mockResolvedValue(
      jsonRes({ reachable: true, directions: ["N", "E"] }),
    );
    await internals.routeToCell(room, room, "insert");
    const text = 'Route to "Длинная дорога": N;E';
    expect(status(pane).textContent).toContain(text);
    await vi.advanceTimersByTimeAsync(statusAutoHideDelay(text));
    expect(status(pane).style.display).toBe("none");
  });

  it("does not let an old route timer erase a newer sticky status and clears it on dispose", async () => {
    fetchMock.mockResolvedValue(
      jsonRes({ reachable: true, directions: ["N"], summary: "1 step" }),
    );
    await internals.routeToCell(room, room, "insert");
    internals.setStatus("Важно: карта меняется", { sticky: true });
    await vi.advanceTimersByTimeAsync(20_000);
    expect(status(pane).textContent).toContain("Важно: карта меняется");
    pane.dispose();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("keeps route warnings visible instead of scheduling them away", async () => {
    fetchMock.mockResolvedValue(
      jsonRes({ reachable: true, directions: ["N"], summary: "1 step", dt: true }),
    );
    await internals.routeToCell(room, room, "insert");
    expect(status(pane).textContent).toContain("death trap on path");
    await vi.advanceTimersByTimeAsync(20_000);
    expect(status(pane).style.display).not.toBe("none");
  });

  it("surfaces and keeps a locked-door route warning", async () => {
    fetchMock.mockResolvedValue(
      jsonRes({ reachable: true, directions: ["N"], summary: "1 step", locked: 1 }),
    );
    await internals.routeToCell(room, room, "insert");
    expect(status(pane).textContent).toContain("a key is required");
    await vi.advanceTimersByTimeAsync(20_000);
    expect(status(pane).style.display).not.toBe("none");
  });

  it("does not flatten delayed opening steps into an unsafe chat script", async () => {
    fetchMock.mockResolvedValue(jsonRes({
      reachable: true,
      directions: ["w", "повернуть рычаг", "d"],
      delayed_openings: 1,
      open_delay_ms: 2000,
    }));
    await internals.routeToCell(room, room, "insert");
    expect(internals.config.setInputText).not.toHaveBeenCalled();
    expect(status(pane).textContent).toContain("2000 ms of opening waits");
    await vi.advanceTimersByTimeAsync(20_000);
    expect(status(pane).style.display).not.toBe("none");
  });
});
