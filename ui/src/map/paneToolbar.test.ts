import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mainStyles = readFileSync(resolve(process.cwd(), "src/styles/main.css"), "utf8");

const fetchMock = vi.fn();
vi.mock("../dom", () => ({ fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init) }));

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

function remoteRoom(): SlimRoom {
  return { ...room(), z: "B", h: "Remote Atrium", x: 7, y: 9, l: 2 };
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
      if (url.includes("/rooms/search")) {
        return Promise.resolve(json(url.includes("remote")
          ? [{ z: "B", h: "Remote Atrium", desc: "A narrow ledge above the ruins.", x: 7, y: 9, l: 2 }]
          : [{ z: "A", h: "Atrium", x: 0, y: 0, l: 0 }]));
      }
      if (url.includes("/zones")) return Promise.resolve(json([{ zone: "A", room_count: 2 }, { zone: "B", room_count: 1 }]));
      if (url.includes("zone=A")) return Promise.resolve(json([room(), upperRoom()]));
      if (url.includes("zone=B")) return Promise.resolve(json([remoteRoom()]));
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

  it("searches across zones and opens the selected room", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const renderer = (pane as unknown as { renderer: { centerOn(x: number, y: number, l: number): void } }).renderer;
    const centerOn = vi.spyOn(renderer, "centerOn");
    pane.root.querySelector<HTMLButtonElement>('[aria-label="Find room"]')!.click();
    const input = pane.root.querySelector<HTMLInputElement>(".map-pane__search")!;
    input.value = "remote";
    input.dispatchEvent(new Event("input"));

    const result = await vi.waitFor(() => {
      const button = pane.root.querySelector<HTMLButtonElement>(".map-pane__search-results button");
      expect(button?.textContent).toContain("Remote Atrium");
      expect(button?.textContent).toContain("B");
      expect(button?.textContent).toContain("A narrow ledge above the ruins.");
      expect(button?.textContent).toContain("7,9,2");
      return button!;
    }, { timeout: 1000 });
    result.click();

    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("B"));
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("zone=B"))).toBe(true);
    expect(centerOn).toHaveBeenCalledWith(7, 9, 2);
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

  it("renders OBS lifecycle states, fences stale updates, and resets for restore", async () => {
	const button = [...pane.root.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === "OBS")!;
	expect(button.classList.contains("map-pane__recording-toggle")).toBe(true);
	expect(mainStyles).toMatch(/\.map-pane__icon\.map-pane__recording-toggle\s*\{[^}]*width:\s*auto;[^}]*flex:\s*0 0 auto;[^}]*padding:\s*0 6px;/s);
    pane.onMapRecordingState({ mode: "armed", pending: 0, revision: 5 });
    expect(button.textContent).toBe("STOP OBS");
    expect(button.getAttribute("aria-pressed")).toBe("true");
    pane.onMapRecordingState({ mode: "off", pending: 0, revision: 4 });
    expect(button.textContent).toBe("STOP OBS");
    pane.onMapRecordingState({ mode: "recording", pending: 1, revision: 6 });
    expect(button.title).toContain("wait for the new cell to turn green");
    pane.onMapRecordingState({ mode: "conflict", pending: 0, revision: 7, reason: "mismatch" });
    expect(button.textContent).toBe("OBS!");
    expect(button.getAttribute("aria-pressed")).toBe("false");
    pane.resetMapRecordingState({ mode: "off", pending: 0, revision: 0 });
    pane.onMapRecordingState({ mode: "armed", pending: 0, revision: 1 });
    expect(button.textContent).toBe("STOP OBS");

    pane.resetMapRecordingState({ mode: "off", pending: 0, revision: 0 });
    fetchMock.mockResolvedValueOnce({ ok: false, status: 409, json: async () => ({ reason: "green confirmed anchor required" }) } as Response);
    button.click();
    await vi.waitFor(() => expect(pane.root.textContent).toContain("Recording control failed: green confirmed anchor required"));
  });

  it("keeps reconciliation apply disabled until a server preview and preserves stale dialog", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const conflict = { id: 9, move: { command: "climb exact", direction: "N", source: "input", from: { zone:"A",x:1,y:0,l:1 }, expected: { zone:"A",x:0,y:0,l:0 } }, expected:{zone:"A",x:0,y:0,l:0}, expected_hint:"Expected", expected_room:{hint:"Expected",desc:"saved description",exits:"N"}, observed:{hint:"Atrium",desc:"seen description",exits:"S"}, reason:"mismatch" };
    pane.onMapRecordingState({mode:"conflict",pending:0,revision:20,conflict});
    const dialog=pane.root.querySelector<HTMLDialogElement>('[aria-label="Resolve mapper conflict"]')!;
    expect(dialog.querySelector(".map-reconciliation-diff")?.textContent).toContain("Expected: saved description");expect(dialog.querySelector(".map-reconciliation-diff")?.textContent).toContain("Observed: seen description");expect(mainStyles).toMatch(/\.map-reconciliation-diff\s*\{[^}]*max-height:[^}]*overflow:\s*auto/s);
    expect(document.activeElement).toBe(dialog.querySelector("select"));
    const buttons=[...dialog.querySelectorAll<HTMLButtonElement>("button")];const preview=buttons.find(b=>b.textContent==="Preview")!;const apply=buttons.find(b=>b.textContent==="Apply")!;
    expect(apply.disabled).toBe(true);
    fetchMock.mockResolvedValueOnce(json({token:"server-token",command:"climb exact",target:{zone:"A",x:0,y:0,l:0}}));preview.click();await vi.waitFor(()=>expect(apply.disabled).toBe(false));
    fetchMock.mockResolvedValueOnce({ok:false,status:409,text:async()=>"stale topology revision"} as Response);apply.click();await vi.waitFor(()=>expect(dialog.textContent).toContain("stale topology revision"));expect(pane.root.contains(dialog)).toBe(true);expect(apply.disabled).toBe(true);
    const cancel=buttons.find(b=>b.textContent==="Cancel")!;cancel.focus();cancel.dispatchEvent(new KeyboardEvent("keydown",{key:"Tab",bubbles:true}));expect(document.activeElement).toBe(dialog.querySelector("select"));
    dialog.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true}));expect(pane.root.querySelector('[aria-label="Resolve mapper conflict"]')).toBeNull();expect(document.activeElement?.textContent).toBe("OBS!");
  });

  it("can discard an observation and stop OBS from the conflict dialog", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const conflict = { id: 91, move: { command: "north", direction: "N", source: "input", from: { zone:"A",x:1,y:0,l:1 }, expected: { zone:"A",x:0,y:0,l:0 } }, expected:{zone:"A",x:0,y:0,l:0}, expected_hint:"Expected", expected_room:{hint:"Expected",desc:"visible",exits:"S"}, observed:{hint:"prompt",desc:"dark",exits:"N S"}, reason:"mismatch" };
    pane.onMapRecordingState({mode:"conflict",pending:0,revision:30,conflict});
    const dialog=pane.root.querySelector<HTMLDialogElement>('[aria-label="Resolve mapper conflict"]')!;
    const discard=[...dialog.querySelectorAll<HTMLButtonElement>("button")].find(button=>button.textContent==="Discard observation and stop OBS");
    expect(discard).toBeDefined();
    fetchMock.mockResolvedValueOnce(json({stopped:true,state:{mode:"off",pending:0,revision:31,reason:"stopped by user"}}));
    discard!.click();
    await vi.waitFor(()=>expect(pane.root.querySelector('[aria-label="Resolve mapper conflict"]')).toBeNull());
    expect(fetchMock.mock.calls.some(([url])=>String(url).endsWith('/map-recording-stop'))).toBe(true);
    expect(pane.root.querySelector<HTMLButtonElement>('.map-pane__recording-toggle')?.textContent).toBe('OBS');
  });

  it("offers an exit-only mutable-room confirmation and resumes OBS", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const conflict = { id: 92, move: { command: "south", direction: "S", source: "input", from: { zone:"A",x:-1,y:0,l:0 }, expected: { zone:"A",x:0,y:0,l:0 } }, expected:{zone:"A",x:0,y:0,l:0}, expected_hint:"Atrium", expected_room:{hint:"Atrium",desc:"same",exits:"N"}, observed:{hint:"Atrium",desc:"same",exits:"N D"}, reason:"mismatch", mutable_exit_candidates:["D"], expected_mutable_exits:[] };
    pane.onMapRecordingState({mode:"conflict",pending:0,revision:32,conflict});
    const dialog=pane.root.querySelector<HTMLDialogElement>('[aria-label="Resolve mapper conflict"]')!;
    expect(dialog.textContent).toContain("Only exits differ");
    const accept=[...dialog.querySelectorAll<HTMLButtonElement>("button")].find(button=>button.textContent==="Mark D mutable and continue OBS")!;
    fetchMock.mockResolvedValueOnce(json({applied:true,state:{mode:"recording",pending:0,revision:33}}));
    accept.click();
    await vi.waitFor(()=>expect(pane.root.querySelector('[aria-label="Resolve mapper conflict"]')).toBeNull());
    const call=fetchMock.mock.calls.find(([url])=>String(url).endsWith('/map-reconciliation/accept-mutable-exits'))!;
    expect(JSON.parse(String((call[1] as RequestInit).body))).toEqual({conflict_id:92,directions:["D"]});
    expect(pane.root.querySelector<HTMLButtonElement>('.map-pane__recording-toggle')?.textContent).toBe('STOP OBS');
  });

  it("edits persisted mutable directions from the cell menu", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const editable = { ...room(), me: ["D"] };
    (pane as unknown as { openCellMenu(cell: {z:string;x:number;y:number;l:number}, room: SlimRoom, x:number, y:number): void }).openCellMenu(editable, editable, 20, 20);
    const details=pane.root.querySelector<HTMLDetailsElement>('.map-cell-menu__mutable-exits')!;
    expect(details.querySelector('summary')?.textContent).toBe('Mutable exits…');
    const checked=[...details.querySelectorAll<HTMLInputElement>('input:checked')];
    expect(checked.map(input=>input.value)).toEqual(["D"]);
    details.querySelector<HTMLInputElement>('input[value="U"]')!.checked=true;
    fetchMock.mockResolvedValueOnce(json({ok:true}));
    details.querySelector<HTMLButtonElement>('.map-cell-menu__mutable-save')!.click();
    await vi.waitFor(()=>expect(pane.root.textContent).toContain('Mutable exits saved'));
    const call=fetchMock.mock.calls.find(([url,init])=>String(url).endsWith('/map-cell-patch') && String((init as RequestInit | undefined)?.body).includes('mutable_exits'))!;
    expect(JSON.parse(String((call[1] as RequestInit).body)).mutable_exits).toEqual(["U","D"]);
  });

  it("shows why OBS stopped when the room is obscured", () => {
    pane.onMapRecordingState({mode:"recording",pending:1,revision:40});
    pane.onMapRecordingState({mode:"off",pending:0,revision:41,reason:"Room is dark — restore visibility.",reason_code:"mapper.recording.room_obscured"});
    expect(pane.root.textContent).toContain("Room is dark — restore visibility.");
    expect(pane.root.querySelector<HTMLButtonElement>('.map-pane__recording-toggle')?.textContent).toBe('OBS');
  });

  it("loads the expected cross-zone room before offering reconciliation targets", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    const conflict = {
      id: 10,
      move: { command: "portal", direction: "N", source: "input", from: { zone: "A", x: 1, y: 0, l: 1 }, expected: { zone: "B", x: 7, y: 9, l: 2 } },
      expected: { zone: "B", x: 7, y: 9, l: 2 },
      expected_hint: "Remote expected",
      expected_room: { hint: "Remote Atrium", desc: "", exits: "N" },
      observed: { hint: "Atrium", desc: "", exits: "S" }, reason: "mismatch",
    };
    pane.onMapRecordingState({ mode: "conflict", pending: 0, revision: 21, conflict });
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes("zone=B"))).toBe(true));
    await vi.waitFor(() => expect(pane.root.querySelector('[aria-label="Resolve mapper conflict"]')).not.toBeNull());
    const dialog = pane.root.querySelector<HTMLDialogElement>('[aria-label="Resolve mapper conflict"]')!;
    const select = dialog.querySelector<HTMLSelectElement>("select")!;
    await vi.waitFor(() => expect([...select.options].some((option) => option.value.includes('"zone":"B"'))).toBe(true));
    expect([...select.options].every((option) => option.value.includes('"zone":"B"'))).toBe(true);
  });

  it("keeps the explicit expected-zone load across a position-driven view generation", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    let resolveB!: (response: Response) => void;
    const delayedB = new Promise<Response>((resolve) => { resolveB = resolve; });
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(json([{ zone: "A", room_count: 2 }, { zone: "B", room_count: 1 }]));
      if (url.includes("zone=B")) return delayedB;
      if (url.includes("zone=A")) return Promise.resolve(json([room(), upperRoom()]));
      throw new Error(`unexpected ${url}`);
    });
    const conflict = {
      id: 11,
      move: { command: "portal", direction: "N", source: "input", from: { zone: "A", x: 1, y: 0, l: 1 }, expected: { zone: "B", x: 7, y: 9, l: 2 } },
      expected: { zone: "B", x: 7, y: 9, l: 2 },
      expected_hint: "Remote expected",
      expected_room: { hint: "Remote Atrium", desc: "", exits: "N" },
      observed: { hint: "Atrium", desc: "", exits: "S" }, reason: "mismatch",
    };
    pane.onMapRecordingState({ mode: "conflict", pending: 0, revision: 22, conflict });
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes("zone=B"))).toBe(true));

    await pane.reload({ preserveView: true });
    resolveB(json([remoteRoom()]));
    await vi.waitFor(() => expect(pane.root.querySelector('[aria-label="Resolve mapper conflict"]')).not.toBeNull());
  });

  it("keeps a newer reconciliation target when an older zone response arrives late", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("A"));
    let resolveB!: (response: Response) => void;
    const delayedB = new Promise<Response>((resolve) => { resolveB = resolve; });
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("map-live-room")) return Promise.resolve(json({ seen: false }));
      if (url.includes("/zones")) return Promise.resolve(json([{ zone: "A", room_count: 2 }, { zone: "B", room_count: 1 }]));
      if (url.includes("zone=B")) return delayedB;
      if (url.includes("zone=A")) return Promise.resolve(json([room(), upperRoom()]));
      throw new Error(`unexpected ${url}`);
    });
    const conflict = (id: number, zone: string, x: number, y: number, l: number) => ({
      id,
      move: { command: "portal", direction: "N", source: "input", from: { zone: "A", x: 1, y: 0, l: 1 }, expected: { zone, x, y, l } },
      expected: { zone, x, y, l },
      expected_hint: "Expected",
      expected_room: { hint: "Expected", desc: "", exits: "N" },
      observed: { hint: "Atrium", desc: "", exits: "S" }, reason: "mismatch",
    });
    pane.onMapRecordingState({ mode: "conflict", pending: 0, revision: 30, conflict: conflict(30, "B", 7, 9, 2) });
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes("zone=B"))).toBe(true));

    pane.onMapRecordingState({ mode: "conflict", pending: 0, revision: 31, conflict: conflict(31, "A", 0, 0, 0) });
    const dialog = await vi.waitFor(() => pane.root.querySelector<HTMLDialogElement>('[aria-label="Resolve mapper conflict"]'));
    const select = dialog!.querySelector<HTMLSelectElement>("select")!;
    expect([...select.options].every((option) => option.value.includes('"zone":"A"'))).toBe(true);
    resolveB(json([remoteRoom()]));
    await Promise.resolve();
    await Promise.resolve();
    expect([...select.options].every((option) => option.value.includes('"zone":"A"'))).toBe(true);
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
