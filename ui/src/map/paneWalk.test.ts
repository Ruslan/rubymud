import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();
vi.mock("../dom", () => ({
  fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init),
}));

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
  z: "Z", t: 2, h: "Target", x: 2, y: 1, l: 0,
  e: [], d: [], ch: 0, se: [], p: false, i: null, s: false,
  dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [],
};

interface PaneInternals {
  routeToCell: (
    cell: { z: string; x: number; y: number; l: number },
    room: SlimRoom,
    action: "insert" | "send",
  ) => Promise<void>;
}

describe("map pane server walk", () => {
  let pane: MapPaneController;
  let internals: PaneInternals;

  beforeEach(() => {
    stubCanvas();
    fetchMock.mockReset();
    pane = new MapPaneController({
      sessionID: 7,
      getActiveMapSetID: () => null,
    });
    document.body.appendChild(pane.root);
    internals = pane as unknown as PaneInternals;
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    vi.restoreAllMocks();
  });

  it("starts the paced server walk and renders its progress", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === "/api/sessions/7/map-walk") {
        return Promise.resolve(jsonRes({ active: true, job_id: 9, current: 0, total: 3, state: "running" }));
      }
      return Promise.resolve(jsonRes({ seen: false }));
    });

    await internals.routeToCell(room, room, "send");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/sessions/7/map-walk",
      expect.objectContaining({ method: "POST" }),
    );
    const call = fetchMock.mock.calls.find(([url]) => url === "/api/sessions/7/map-walk");
    const init = call?.[1] as RequestInit;
    expect(JSON.parse(init.body as string)).toEqual({
      to: { zone: "Z", x: 2, y: 1, l: 0 },
    });
    const progress = pane.root.querySelector<HTMLElement>(".map-pane__walk-progress");
    expect(progress?.textContent).toBe("Walk 1/3");
    expect(progress?.title).toBe("Walk 1/3");
    const stop = pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop");
    expect(stop?.hidden).toBe(false);
    expect(stop?.disabled).toBe(false);
  });

  it("keeps Stop available from websocket progress and posts a dedicated cancel", async () => {
    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: "running" });
    fetchMock.mockImplementation((url: string) => {
      if (url === "/api/sessions/7/map-walk/cancel") {
        return Promise.resolve(jsonRes({ active: false, job_id: 4, current: 1, total: 3, state: "stopped", reason: "cancelled by user" }));
      }
      return Promise.resolve(jsonRes({ seen: false }));
    });

    pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.click();
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/sessions/7/map-walk/cancel",
        { method: "POST" },
      );
    });

    await vi.waitFor(() => {
      expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(true);
    });
  });

  it("renders a paused walk and resumes only when the server permits it", async () => {
    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: "paused", reason: "movement refused", can_resume: false });
    const resume = pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-resume");
    expect(pane.root.querySelector(".map-pane__walk-progress")?.textContent).toBe("Walk 1/3: movement refused");
    expect(pane.root.querySelector<HTMLElement>(".map-pane__walk-progress")?.title).toBe("Walk 1/3: movement refused");
    expect(resume?.hidden).toBe(false);
    expect(resume?.disabled).toBe(true);
		expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(false);

    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: "paused", reason: "movement refused", can_resume: true });
    fetchMock.mockResolvedValue(jsonRes({ active: true, job_id: 5, current: 0, total: 2, state: "running" }));
    resume?.click();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/sessions/7/map-walk/resume", { method: "POST" },
    ));
  });

  it("rejects a stale paused eligibility snapshot by revision", () => {
    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: "paused", can_resume: false, revision: 10 });
    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: "paused", can_resume: true, revision: 11 });
    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: "paused", can_resume: false, revision: 10 });

    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-resume")?.disabled).toBe(false);
  });

  it("does not resurrect a completed walk from a delayed REST response", () => {
    pane.onMapWalk({ active: true, job_id: 12, current: 0, total: 1, state: "running" });
    pane.onMapWalk({ active: false, job_id: 12, current: 1, total: 1, state: "stopped", reason: "completed" });
    pane.onMapWalk({ active: true, job_id: 12, current: 0, total: 1, state: "running" });

    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(true);
  });

  it("accepts a reused job id from a newer MUD session epoch", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 1, current: 0, total: 1, state: "running" });
    pane.onMapWalk({ active: false, epoch: 41, job_id: 1, current: 1, total: 1, state: "stopped" });
    pane.onMapWalk({ active: true, epoch: 42, job_id: 1, current: 0, total: 1, state: "running" });

    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(false);
  });

  it("does not roll active progress backward from a delayed REST or restore snapshot", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 1, total: 3, state: "running" });
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 0, total: 3, state: "running" });

    expect(pane.root.querySelector(".map-pane__walk-progress")?.textContent).toBe("Walk 2/3");
  });

  it("does not replace stopping state with an older running snapshot", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 1, total: 3, state: "stopping" });
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 1, total: 3, state: "running" });

    expect(pane.root.querySelector(".map-pane__walk-progress")?.textContent).toBe("Stopping 1/3");
  });

  it("clears a cached active walk from an explicit no-active reconnect snapshot", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 0, total: 3, state: "running" });
    pane.onMapWalk({ active: false, epoch: 41, job_id: 8, current: 0, total: 0, state: "stopped" });

    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(true);
    expect(pane.root.querySelector<HTMLElement>(".map-pane__walk-progress")?.title).toBe("");
  });

  it("ignores a delayed id-zero inactive response after a live job starts", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 9, current: 0, total: 2, state: "running", revision: 1 });
    pane.onMapWalk({ active: false, epoch: 41, job_id: 0, current: 0, total: 0, state: "stopped", reason: "no paused route" });
    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(false);
  });

  it("accepts an id-zero inactive state for a pristine session", () => {
    pane.onMapWalk({ active: false, epoch: 41, job_id: 0, current: 0, total: 0, state: "stopped" });
    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(true);
  });

  it("ignores an older active job after a newer active job", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 9, current: 0, total: 2, state: "running" });
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 0, total: 3, state: "running" });
    expect(pane.root.querySelector(".map-pane__walk-progress")?.textContent).toBe("Walk 1/2");
  });

  it("ignores an older cancel response while a newer job is active", () => {
    pane.onMapWalk({ active: true, epoch: 41, job_id: 9, current: 0, total: 2, state: "running" });
    pane.onMapWalk({ active: false, epoch: 41, job_id: 8, current: 0, total: 2, state: "stopped", reason: "cancelled" });
    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(false);
  });

  it("uses an inactive restore high-water to reject a delayed same-job start", () => {
    pane.onMapWalk({ active: false, epoch: 41, job_id: 8, current: 0, total: 0, state: "stopped" });
    pane.onMapWalk({ active: true, epoch: 41, job_id: 8, current: 0, total: 2, state: "running" });
    expect(pane.root.querySelector<HTMLButtonElement>(".map-pane__walk-stop")?.hidden).toBe(true);
  });
});
