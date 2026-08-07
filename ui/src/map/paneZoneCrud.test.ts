import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();
vi.mock("../dom", () => ({ fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init) }));

import { MapPaneController } from "./pane";
import type { SlimRoom } from "./types";

function json(body: unknown): Response {
  return { ok: true, json: async () => body, text: async () => JSON.stringify(body) } as unknown as Response;
}
function stubCanvas() {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => new Proxy(
    { fillStyle: "", strokeStyle: "", lineWidth: 0, font: "", textAlign: "", textBaseline: "" },
    { get: (target, property) => property in target ? (target as never)[property] : () => {} },
  ) as unknown as CanvasRenderingContext2D);
}
function room(overrides: Partial<SlimRoom> = {}): SlimRoom {
  return { z: "A", t: null, h: "room", x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [], p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [], ...overrides };
}
const preview = { revision: 7, rooms: 2, internal_edges: 1, boundary_grid_edges: 0, portals: 0, incoming: 0, outgoing: 0, cross_zone_contacts: 0, components: 1, disconnected: false, annotations_moved: 0, collision_count: 0, source_empties: false, target_exists: true };

describe("map pane zone CRUD dialog", () => {
  let pane: MapPaneController;
  beforeEach(() => {
    stubCanvas();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(json([{ zone: "A", room_count: 2 }, { zone: "B", room_count: 1 }]));
      if (url.includes("zone=A")) return Promise.resolve(json([room(), room({ x: 2, y: 1 })]));
      throw new Error(`unexpected ${url}`);
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => 1 });
    document.body.appendChild(pane.root);
  });
  afterEach(() => { pane.dispose(); pane.root.remove(); vi.restoreAllMocks(); });

  it("previews and applies only the exact canonical selection, then refreshes once", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const renderer = (pane as unknown as { renderer: { onSelectionChange?: (coords: Array<{ x: number; y: number; l: number }>) => void } }).renderer;
    renderer.onSelectionChange?.([{ x: 2, y: 1, l: 0 }, { x: 0, y: 0, l: 0 }, { x: 2, y: 1, l: 0 }]);
    const move = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Move to zone…") as HTMLButtonElement;
    move.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-zone-dialog")!;
    const select = dialog.querySelector<HTMLSelectElement>("select")!;
    select.value = "B";
    select.dispatchEvent(new Event("change"));
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-zones/preview")) return Promise.resolve(json({ ok: true, active_map_set: 1, preview }));
      if (url.includes("map-zones/apply")) return Promise.resolve(json({ ok: true, active_map_set: 2, forked_to: 2, preview }));
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(json([{ zone: "A", room_count: 1 }, { zone: "B", room_count: 2 }]));
      if (url.includes("zone=A")) return Promise.resolve(json([room()]));
      throw new Error(`unexpected ${url}`);
    });
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Preview")!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("2 rooms"));
    const previewCall = fetchMock.mock.calls.find(([url]) => String(url).includes("map-zones/preview"))!;
    expect(JSON.parse(previewCall[1].body)).toEqual({ source_zone: "A", target_zone: "B", rooms: [{ x: 0, y: 0, l: 0 }, { x: 2, y: 1, l: 0 }] });
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Apply")!.click();
    await vi.waitFor(() => expect(pane.root.querySelector(".map-zone-dialog")).toBeNull());
    const applyCall = fetchMock.mock.calls.find(([url]) => String(url).includes("map-zones/apply"))!;
    expect(JSON.parse(applyCall[1].body)).toEqual({ source_zone: "A", target_zone: "B", rooms: [{ x: 0, y: 0, l: 0 }, { x: 2, y: 1, l: 0 }], expected_revision: 7 });
    await vi.waitFor(() => expect((pane as unknown as { mapSetID: number }).mapSetID).toBe(2));
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/map-sets/2/zones"))).toBe(true);
  });

  it("renames server-side without sending coordinates and restores focus on Escape", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const rename = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Rename zone…") as HTMLButtonElement;
    rename.focus(); rename.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-zone-dialog")!;
    const input = [...dialog.querySelectorAll<HTMLInputElement>("input")].find((el) => el.type === "text")!;
    input.value = "Renamed"; input.dispatchEvent(new Event("input"));
    fetchMock.mockImplementation((url: string) => url.includes("rename/preview")
      ? Promise.resolve(json({ ok: true, active_map_set: 1, preview: { ...preview, source_empties: true } }))
      : Promise.resolve(json({ seen: false })));
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Preview")!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("2 rooms"));
    const call = fetchMock.mock.calls.find(([url]) => String(url).includes("rename/preview"))!;
    expect(JSON.parse(call[1].body)).toEqual({ source_zone: "A", target_zone: "Renamed" });
    dialog.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(pane.root.querySelector(".map-zone-dialog")).toBeNull();
    expect(document.activeElement).toBe(rename);
  });

  it("keeps the form and selection when a stale revision requires a new preview", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const renderer = (pane as unknown as { renderer: { onSelectionChange?: (coords: Array<{ x: number; y: number; l: number }>) => void } }).renderer;
    (renderer as unknown as { selected: Set<string> }).selected.add("0,0,0");
    renderer.onSelectionChange?.([{ x: 0, y: 0, l: 0 }]);
    const move = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Move to zone…") as HTMLButtonElement;
    move.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-zone-dialog")!;
    const select = dialog.querySelector<HTMLSelectElement>("select")!;
    select.value = "B"; select.dispatchEvent(new Event("change"));
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-zones/preview")) return Promise.resolve(json({ ok: false, reason: "stale topology revision: expected 7, current 8" }));
      if (url.includes("zone=A")) return Promise.resolve(json([room(), room({ x: 2, y: 1 })]));
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      throw new Error(`unexpected ${url}`);
    });
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Preview")!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("Topology changed; preview again."));
    expect(select.value).toBe("B");
    expect(pane.root.querySelector(".map-pane__selection-count")?.textContent).toBe("1");
  });

  it("clears selection on the first Escape and exits selection mode on the second", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const toggle = pane.root.querySelector<HTMLButtonElement>(".map-pane__tool")!;
    toggle.click();
    const renderer = (pane as unknown as { renderer: { selected: Set<string>; onSelectionChange?: (coords: Array<{ x: number; y: number; l: number }>) => void } }).renderer;
    renderer.selected.add("0,0,0");
    renderer.onSelectionChange?.([{ x: 0, y: 0, l: 0 }]);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(pane.root.querySelector(".map-pane__selection-count")?.textContent).toBe("");
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
  });

  it("retains selected rooms across a same-zone rooms_changed refresh", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const renderer = (pane as unknown as { renderer: { selected: Set<string>; onSelectionChange?: (coords: Array<{ x: number; y: number; l: number }>) => void } }).renderer;
    renderer.selected.add("0,0,0");
    renderer.onSelectionChange?.([{ x: 0, y: 0, l: 0 }]);
    await pane.refreshRooms();
    expect(pane.root.querySelector(".map-pane__selection-count")?.textContent).toBe("1");
  });

  it("rejects control characters locally without issuing a preview request", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const rename = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Rename zone…") as HTMLButtonElement;
    rename.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-zone-dialog")!;
    const input = [...dialog.querySelectorAll<HTMLInputElement>("input")].find((el) => el.type === "text")!;
    input.value = "bad\u0001zone";
    input.dispatchEvent(new Event("input"));
    const before = fetchMock.mock.calls.length;
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Preview")!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("control character"));
    expect(fetchMock.mock.calls).toHaveLength(before);
  });

  it("keeps only the latest zone-list response after close and reopen", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    let resolveOld!: (response: Response) => void;
    let resolveNew!: (response: Response) => void;
    const oldList = new Promise<Response>((resolve) => { resolveOld = resolve; });
    const newList = new Promise<Response>((resolve) => { resolveNew = resolve; });
    let listCall = 0;
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("/zones")) return ++listCall === 1 ? oldList : newList;
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      throw new Error(`unexpected ${url}`);
    });
    const rename = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Rename zone…") as HTMLButtonElement;
    rename.click();
    pane.root.querySelector<HTMLElement>(".map-zone-dialog")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    rename.click();
    resolveOld(json([{ zone: "OLD", room_count: 1 }]));
    resolveNew(json([{ zone: "A", room_count: 2 }, { zone: "NEW", room_count: 1 }]));
    await vi.waitFor(() => expect([...pane.root.querySelectorAll(".map-zone-dialog option")].map((option) => option.textContent)).toContain("NEW"));
    expect([...pane.root.querySelectorAll(".map-zone-dialog option")].map((option) => option.textContent)).not.toContain("OLD");
  });

  it("closes a source-bound dialog when the displayed zone changes", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const rename = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Rename zone…") as HTMLButtonElement;
    rename.click();
    const zone = pane.root.querySelector<HTMLSelectElement>(".map-pane__zone")!;
    zone.value = "B";
    zone.dispatchEvent(new Event("change"));
    expect(pane.root.querySelector(".map-zone-dialog")).toBeNull();
  });

  it("does not let a new dialog race the authoritative post-apply refresh", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const renderer = (pane as unknown as { renderer: { selected: Set<string>; onSelectionChange?: (coords: Array<{ x: number; y: number; l: number }>) => void } }).renderer;
    renderer.selected.add("0,0,0");
    renderer.onSelectionChange?.([{ x: 0, y: 0, l: 0 }]);
    const move = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Move to zone…") as HTMLButtonElement;
    move.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-zone-dialog")!;
    const select = dialog.querySelector<HTMLSelectElement>("select")!;
    select.value = "B";
    select.dispatchEvent(new Event("change"));

    let resolveZones!: (response: Response) => void;
    const zones = new Promise<Response>((resolve) => { resolveZones = resolve; });
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-zones/preview")) return Promise.resolve(json({ ok: true, active_map_set: 1, preview }));
      if (url.includes("map-zones/apply")) return Promise.resolve(json({ ok: true, active_map_set: 1, preview }));
      if (url.includes("/zones")) return zones;
      if (url.includes("zone=A")) return Promise.resolve(json([room()]));
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      throw new Error(`unexpected ${url}`);
    });
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Preview")!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("2 rooms"));
    [...dialog.querySelectorAll("button")].find((button) => button.textContent === "Apply")!.click();
    await vi.waitFor(() => expect(pane.root.querySelector(".map-zone-dialog")).toBeNull());

    const rename = [...pane.root.querySelectorAll("button")].find((button) => button.textContent === "Rename zone…") as HTMLButtonElement;
    expect(rename.disabled).toBe(true);
    rename.click();
    expect(pane.root.querySelector(".map-zone-dialog")).toBeNull();
    resolveZones(json([{ zone: "A", room_count: 1 }, { zone: "B", room_count: 2 }]));
    await vi.waitFor(() => expect(rename.disabled).toBe(false));
  });
});
