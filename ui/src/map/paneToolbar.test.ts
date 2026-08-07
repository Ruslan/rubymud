import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mainStyles = readFileSync(resolve(process.cwd(), "src/styles/main.css"), "utf8");

const fetchMock = vi.fn();
vi.mock("../dom", () => ({ fetchWithToken: (url: string) => fetchMock(url) }));

import { MapPaneController } from "./pane";
import type { SlimRoom } from "./types";

function json(body: unknown): Response {
  return { ok: true, json: async () => body, text: async () => JSON.stringify(body) } as unknown as Response;
}

function room(): SlimRoom {
  return { z: "A", t: null, h: "Atrium", x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [], p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [] };
}

function upperRoom(): SlimRoom {
  return { ...room(), h: "Gallery", x: 1, l: 1 };
}

function stubCanvas(): void {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => new Proxy(
    { fillStyle: "", strokeStyle: "", lineWidth: 0, font: "", textAlign: "", textBaseline: "" },
    { get: (target, property) => property in target ? (target as never)[property] : () => {} },
  ) as unknown as CanvasRenderingContext2D);
}

describe("map pane compact toolbar", () => {
  let pane: MapPaneController;

  beforeEach(() => {
    stubCanvas();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(json([{ zone: "A", room_count: 1 }]));
      if (url.includes("zone=A")) return Promise.resolve(json([room(), upperRoom()]));
      throw new Error(`unexpected ${url}`);
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => 1 });
    document.body.appendChild(pane.root);
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    document.documentElement.style.removeProperty("--app-font-size");
    document.documentElement.style.removeProperty("--app-line-height");
    vi.restoreAllMocks();
  });

  it("keeps the bar one line and opens an autofocus search popover that closes to its opener", async () => {
    await vi.waitFor(() => expect(pane.root.querySelector(".map-pane__zone")).not.toBeNull());
    const bar = pane.root.querySelector<HTMLElement>(".map-pane__bar")!;
    expect(bar.querySelector('input[type="search"]')).toBeNull();
    const search = pane.root.querySelector<HTMLButtonElement>('[aria-label="Find room"]')!;
    search.click();
    const popover = pane.root.querySelector<HTMLElement>(".map-pane__search-popover")!;
    const input = popover.querySelector<HTMLInputElement>('input[type="search"]')!;
    await Promise.resolve();
    expect(document.activeElement).toBe(input);
    input.value = "atri";
    input.dispatchEvent(new Event("input"));
    await vi.waitFor(() => expect(popover.textContent).toContain("Atrium"));
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(pane.root.querySelector(".map-pane__search-popover")).toBeNull();
    expect(document.activeElement).toBe(search);
  });

  it("puts selection and zone actions behind the manager popover", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const manager = pane.root.querySelector<HTMLButtonElement>('[aria-label="Zone manager"]')!;
    expect(manager.childNodes[0]?.textContent).toBe("⌗");
    manager.click();
    const popover = pane.root.querySelector<HTMLElement>(".map-pane__manager-popover")!;
    expect(popover.textContent).toContain("Select rooms");
    expect(popover.textContent).toContain("Move to zone");
    expect(pane.root.querySelector(".map-pane__bar")?.textContent).not.toContain("Move to zone");
    [...popover.querySelectorAll("button")].find((button) => button.textContent === "Select rooms")!.click();
    expect(pane.root.querySelector(".map-pane__manager-popover")).toBeNull();
    expect(manager.classList.contains("on")).toBe(true);
  });

  it("gives every popover control a stable readable font independent of the output font", async () => {
    document.documentElement.style.setProperty("--app-font-size", "4px");
    document.documentElement.style.setProperty("--app-line-height", "6px");
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));

    pane.root.querySelector<HTMLButtonElement>('[aria-label="Find room"]')!.click();
    const input = pane.root.querySelector<HTMLInputElement>(".map-pane__search")!;
    input.value = "atri";
    input.dispatchEvent(new Event("input"));
    const searchPopover = pane.root.querySelector<HTMLElement>(".map-pane__search-popover")!;
    const result = await vi.waitFor(() => pane.root.querySelector<HTMLButtonElement>(".map-pane__search-results button")!);
    expect(searchPopover.classList.contains("map-pane__popover")).toBe(true);
    expect(input.classList.contains("map-pane__search")).toBe(true);
    expect(result.closest(".map-pane__search-results")).not.toBeNull();
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));

    pane.root.querySelector<HTMLButtonElement>('[aria-label="Zone manager"]')!.click();
    const action = pane.root.querySelector<HTMLElement>(".map-pane__manager-action")!;
    const count = pane.root.querySelector<HTMLElement>(".map-pane__manager-count")!;
    expect(action.closest(".map-pane__popover")).not.toBeNull();
    expect(count.closest(".map-pane__popover")).not.toBeNull();
    expect(mainStyles).toMatch(/\.map-pane__popover\s*\{[^}]*font-size:\s*12px;[^}]*line-height:\s*16px;/s);
    expect(mainStyles).toMatch(/\.map-pane__popover \.map-pane__search\s*\{[^}]*font-size:\s*12px;[^}]*line-height:\s*16px;/s);
    expect(mainStyles).toMatch(/\.map-pane__manager-count\s*\{[^}]*font-size:\s*11px;[^}]*line-height:\s*16px;/s);
    expect(mainStyles).toMatch(/\.map-pane__manager-popover \.map-pane__zone-command\s*\{[^}]*font-size:\s*12px;[^}]*line-height:\s*16px;/s);
  });

  it("keeps the iconified Follow control's live state visible", () => {
    const label = pane.root.querySelector<HTMLElement>(".map-pane__follow")!;
    const input = label.querySelector<HTMLInputElement>("input")!;
    expect(input.checked).toBe(true);
    expect(label.classList.contains("on")).toBe(true);
    input.checked = false;
    input.dispatchEvent(new Event("change"));
    expect(label.classList.contains("on")).toBe(false);
  });

  it("closes either popover when the floor or map-set context changes", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    pane.root.querySelector<HTMLButtonElement>('[aria-label="Find room"]')!.click();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-pane__floors button")]
      .find((button) => button.textContent === "1")!.click();
    expect(pane.root.querySelector(".map-pane__search-popover")).toBeNull();

    pane.root.querySelector<HTMLButtonElement>('[aria-label="Zone manager"]')!.click();
    const internals = pane as unknown as {
      nextLoadGeneration(): number;
      loadMapSet(opts: { mapSetID: number }, generation: number): Promise<void>;
    };
    const generation = internals.nextLoadGeneration();
    await internals.loadMapSet({ mapSetID: 2 }, generation);
    expect(pane.root.querySelector(".map-pane__manager-popover")).toBeNull();
  });

  it("autofocuses the manager action and returns dialog focus to the manager button", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const manager = pane.root.querySelector<HTMLButtonElement>('[aria-label="Zone manager"]')!;
    manager.click();
    const popover = pane.root.querySelector<HTMLElement>(".map-pane__manager-popover")!;
    const select = [...popover.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Select rooms")!;
    await Promise.resolve();
    expect(document.activeElement).toBe(select);

    const rename = [...popover.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent?.startsWith("Rename zone"))!;
    rename.focus();
    rename.click();
    const cancel = [...pane.root.querySelectorAll<HTMLButtonElement>(".map-zone-dialog button")]
      .find((button) => button.textContent === "Cancel")!;
    cancel.click();
    expect(document.activeElement).toBe(manager);
  });
});
