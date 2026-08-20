import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();
vi.mock("../dom", () => ({
  fetchWithToken: (url: string, init?: RequestInit) => fetchMock(url, init),
}));

import { MapPaneController } from "./pane";
import type { SlimRoom } from "./types";

function response(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => typeof body === "string" ? body : JSON.stringify(body),
  } as unknown as Response;
}

function stubCanvas(): void {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
    () => new Proxy(
      { fillStyle: "", strokeStyle: "", lineWidth: 0, font: "", textAlign: "", textBaseline: "" },
      { get: (target, property) => property in target ? (target as never)[property] : () => {} },
    ) as unknown as CanvasRenderingContext2D,
  );
}

const room: SlimRoom = {
  z: "Город", t: 1, h: "Кузница", x: 3, y: 4, l: 0,
  e: [], d: [], ch: 0, se: [], p: false, i: null, s: false,
  dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [],
};

const initialRows = [
  {
    id: 1, placement_id: 11, map_set_id: 5, zone: "Город", x: 3, y: 4, l: 0,
    name: "Кузнец", kind: "npc", look_target: "кузнец", look_text: "Седой мастер.",
    note: "Даёт заказ.", strategy: "Принести руду.", presence_text: "Кузнец у наковальни.", resident: true,
  },
  {
    id: 2, placement_id: 12, map_set_id: 5, zone: "Город", x: 3, y: 4, l: 0,
    name: "Крыса", kind: "monster", look_target: "крыса", look_text: "Голодная крыса.",
    note: "", strategy: "Не кормить.", presence_text: "Крыса шуршит в углу.", resident: false,
  },
];

describe("map room inhabitants knowledge dialog", () => {
  let pane: MapPaneController;
  let nextRoomList: unknown | null;
  let roomListResponses: unknown[];
  let failNextRoomList = false;
  let legacyCursorFailure = false;
  let catalogItems: unknown[];

  beforeEach(() => {
    stubCanvas();
    nextRoomList = null;
    roomListResponses = [];
    failNextRoomList = false;
    legacyCursorFailure = false;
    catalogItems = [{
      id: 8, map_set_id: 5, name: "Странствующий лекарь", kind: "npc",
      look_target: "лекарь", strategy: "Купить бинты.", placement_count: 1,
    }];
    vi.spyOn(window, "confirm").mockReturnValue(true);
    fetchMock.mockImplementation((rawURL: string, init?: RequestInit) => {
      const url = decodeURIComponent(rawURL);
      if (url.includes("map-live-room")) return Promise.resolve(response({ seen: false }));
      if (url.endsWith("/api/map-sets/5/zones")) return Promise.resolve(response([{ zone: "Город", room_count: 1 }]));
      if (url.includes("/api/rooms?map_set=5&zone=Город")) return Promise.resolve(response([room]));
      if (url.includes("/api/map-sets/5/inhabitants") && (!init?.method || init.method === "GET")) {
		if (url.includes("/catalog?")) {
		  return Promise.resolve(response({ items: catalogItems, truncated: false }));
		}
		if (failNextRoomList) {
		  failNextRoomList = false;
		  return Promise.resolve(response("refresh failed", 500));
		}
		if (legacyCursorFailure && url.includes("cursor=")) {
		  legacyCursorFailure = false;
		  return Promise.resolve(response("legacy_room_inhabitant_cursor", 400));
		}
        const body = roomListResponses.shift() ?? nextRoomList ?? initialRows.map((row) => ({ ...row }));
        nextRoomList = null;
        return Promise.resolve(response(body));
      }
      if (url.endsWith("/api/map-sets/5/inhabitants") && init?.method === "POST") {
        const body = JSON.parse(String(init.body));
		if (body.inhabitant_id === 8) {
		  return Promise.resolve(response({
			id: 8, placement_id: 18, map_set_id: 5, name: "Странствующий лекарь", kind: "npc",
			look_target: "лекарь", look_text: "", note: "", strategy: "Купить бинты.", ...body,
		  }, 201));
		}
		if (body.inhabitant_id === 1) {
		  return Promise.resolve(response({
			id: 1, placement_id: 101, map_set_id: 5, name: "Low ID", kind: "npc",
			look_target: "low", look_text: "", note: "", strategy: "", ...body,
		  }, 201));
		}
		if (body.name === "New at page edge") {
		  return Promise.resolve(response({
			id: 999, placement_id: 1999, map_set_id: 5, kind: "npc",
			look_target: "new", look_text: "", note: "", strategy: "", ...body,
		  }, 201));
		}
        return Promise.resolve(response({ id: 3, placement_id: 13, map_set_id: 5, ...body }, 201));
      }
      if (url.endsWith("/api/map-sets/5/inhabitants/1") && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        return Promise.resolve(response({ id: 1, placement_id: 11, map_set_id: 5, ...body }));
      }
      if (url.endsWith("/api/map-sets/5/inhabitants/121") && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        return Promise.resolve(response({ id: 121, placement_id: 121, map_set_id: 5, ...body }));
      }
      if (url.includes("/api/map-sets/5/inhabitants/") && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        const id = Number(url.split("/").at(-1));
        return Promise.resolve(response({ id, placement_id: id + 100, map_set_id: 5, ...body }));
      }
      if (url.includes("/api/map-sets/5/inhabitants/2?") && init?.method === "DELETE") {
        return Promise.resolve(response("", 204));
      }
      if (url.includes("/api/map-sets/5/inhabitants/121?") && init?.method === "DELETE") {
        return Promise.resolve(response("", 204));
      }
      throw new Error(`unexpected request: ${rawURL} ${init?.method ?? "GET"}`);
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => 5 });
    document.body.appendChild(pane.root);
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    vi.restoreAllMocks();
  });

  function clickRoom(target: SlimRoom = room): void {
    const renderer = (pane as unknown as {
      renderer: { onRoomClick?: (room: SlimRoom, x: number, y: number) => void };
    }).renderer;
    renderer.onRoomClick?.(target, 100, 120);
  }

  it("loads only after selecting Knowledge → Inhabitants and supports CRUD", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/inhabitants"))).toBe(false);

    clickRoom();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/inhabitants"))).toBe(false);
    const knowledge = [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!;
    knowledge.click();

    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    expect(dialog.getAttribute("aria-label")).toBe("Knowledge");
    const tab = dialog.querySelector<HTMLElement>('[role="tab"]')!;
    const panel = dialog.querySelector<HTMLElement>('[role="tabpanel"]')!;
    expect(tab.textContent).toBe("Inhabitants");
    expect(tab.getAttribute("aria-controls")).toBe(panel.id);
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    expect(dialog.textContent).toContain("Крыса");
    const listCall = fetchMock.mock.calls.find(([url]) => String(url).includes("/inhabitants?"))!;
    expect(decodeURIComponent(String(listCall[0]))).toContain("zone=Город&x=3&y=4&l=0");

    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Add new")!.click();
    const createForm = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (createForm.elements.namedItem("name") as HTMLInputElement).value = "Торговец";
    (createForm.elements.namedItem("kind") as HTMLSelectElement).value = "other";
    (createForm.elements.namedItem("look_target") as HTMLInputElement).value = "торговец";
    (createForm.elements.namedItem("look_text") as HTMLTextAreaElement).value = "Богатая одежда.";
    (createForm.elements.namedItem("note") as HTMLTextAreaElement).value = "Скупает руду.";
    (createForm.elements.namedItem("strategy") as HTMLTextAreaElement).value = "Торговаться.";
    (createForm.elements.namedItem("presence_text") as HTMLTextAreaElement).value = "Торговец считает монеты.";
    (createForm.elements.namedItem("resident") as HTMLInputElement).checked = true;
    createForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(dialog.textContent).toContain("Торговец"));
    const createCall = fetchMock.mock.calls.find(([, init]) => init?.method === "POST")!;
    expect(JSON.parse(String(createCall[1].body))).toMatchObject({
      zone: "Город", x: 3, y: 4, l: 0, name: "Торговец", kind: "other",
      look_target: "торговец", resident: true,
    });

    const smithCard = [...dialog.querySelectorAll<HTMLElement>(".map-inhabitant-card")]
      .find((card) => card.textContent?.includes("Кузнец"))!;
    [...smithCard.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Edit")!.click();
    const editForm = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (editForm.elements.namedItem("name") as HTMLInputElement).value = "Мастер-кузнец";
    editForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(dialog.textContent).toContain("Мастер-кузнец"));
    const patchCall = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")!;
    expect(patchCall[0]).toBe("/api/map-sets/5/inhabitants/1");
    expect(JSON.parse(String(patchCall[1].body))).toMatchObject({ name: "Мастер-кузнец", zone: "Город", x: 3, y: 4, l: 0 });

    const ratCard = [...dialog.querySelectorAll<HTMLElement>(".map-inhabitant-card")]
      .find((card) => card.textContent?.includes("Крыса"))!;
    [...ratCard.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Delete")!.click();
    await vi.waitFor(() => expect(dialog.textContent).not.toContain("Крыса"));
    const deleteCall = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE")!;
    expect(decodeURIComponent(String(deleteCall[0]))).toContain("/inhabitants/2?zone=Город&x=3&y=4&l=0");

    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Close")!.click();
    await Promise.resolve();
    expect(pane.root.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
  });

  it("announces when the bounded room list is truncated", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    nextRoomList = { items: initialRows, truncated: true };
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.textContent).toContain("Showing the first 20 inhabitants"));
    expect(dialog.querySelector('[role="status"]')?.textContent).toContain("room has more");
    expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(initialRows.length);
  });

  it("loads later pages so every bounded entry remains editable and removable", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    const firstPage = Array.from({ length: 20 }, (_, index) => ({
      ...initialRows[0], id: 101 + index, placement_id: 201 + index, name: `NPC ${String(index).padStart(2, "0")}`,
    }));
    const twentyFirst = { ...firstPage[0], id: 121, placement_id: 221, name: "NPC 20" };
    roomListResponses.push(
      { items: firstPage, truncated: true, next_cursor: "cursor-20" },
      { items: [twentyFirst], truncated: false },
      { items: [{ ...twentyFirst, name: "NPC 20 edited" }], truncated: false },
      { items: [], truncated: false },
      { items: firstPage, truncated: true, next_cursor: "cursor-20" },
    );
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    const more = [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Load more")!;
    more.focus();
    more.click();
    await vi.waitFor(() => expect(dialog.querySelector('[data-inhabitant-id="121"]')).not.toBeNull());
    expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(1);
    expect(document.activeElement).not.toBe(more);

    const card = dialog.querySelector<HTMLElement>('[data-inhabitant-id="121"]')!;
    [...card.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Edit")!.click();
    const editForm = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (editForm.elements.namedItem("name") as HTMLInputElement).value = "NPC 20 edited";
    editForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(dialog.textContent).toContain("NPC 20 edited"));
    expect(fetchMock.mock.calls.some(([url, init]) => String(url).endsWith("/inhabitants/121") && init?.method === "PATCH")).toBe(true);

    const editedCard = dialog.querySelector<HTMLElement>('[data-inhabitant-id="121"]')!;
    editedCard.querySelector<HTMLButtonElement>('button:last-of-type')!.click();
    await vi.waitFor(() => expect(dialog.querySelector('[data-inhabitant-id="121"]')).toBeNull());
    expect(fetchMock.mock.calls.some(([url, init]) => String(url).includes("/inhabitants/121?") && init?.method === "DELETE")).toBe(true);
    const previous = dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__previous')!;
    expect(previous.textContent).toBe("Previous page");
    previous.click();
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    expect(dialog.querySelector('[data-inhabitant-id="121"]')).toBeNull();
  });

  it("reloads a full first page after create and keeps the DOM bounded without a WS echo", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    const firstPage = Array.from({ length: 20 }, (_, index) => ({
      ...initialRows[0], id: 401 + index, placement_id: 501 + index, name: `Existing ${String(index).padStart(2, "0")}`,
    }));
    roomListResponses.push(
      { items: firstPage, truncated: true, next_cursor: "cursor-edge" },
      { items: firstPage, truncated: true, next_cursor: "cursor-edge" },
    );
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    const before = fetchMock.mock.calls.filter(([url]) => String(url).includes("/inhabitants?")).length;
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Add new")!.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (form.elements.namedItem("name") as HTMLInputElement).value = "New at page edge";
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([url]) => String(url).includes("/inhabitants?")).length).toBe(before + 1));
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    expect(dialog.querySelector('[data-inhabitant-id="999"]')).toBeNull();
    const reload = fetchMock.mock.calls.filter(([url]) => String(url).includes("/inhabitants?")).at(-1)?.[0];
    expect(String(reload)).toContain("limit=20");
    expect(String(reload)).not.toContain("cursor=");
    expect(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__previous')?.hidden).toBe(true);
    expect(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__load-more')?.hidden).toBe(false);
  });

  it("closes a successful-write form and offers retry when the authoritative refresh fails", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    roomListResponses.push({
      items: Array.from({ length: 20 }, (_, index) => ({
        ...initialRows[0], id: 101 + index, placement_id: 201 + index, name: `Existing ${String(index).padStart(2, "0")}`,
      })), truncated: true, next_cursor: "cursor-refresh-failure",
    });
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Add new")!.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (form.elements.namedItem("name") as HTMLInputElement).value = "Written before refresh failure";
    const postCount = () => fetchMock.mock.calls.filter(([, init]) => init?.method === "POST").length;
    const writesBefore = postCount();
    failNextRoomList = true;
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-form")).toBeNull());
    expect(postCount()).toBe(writesBefore + 1);
    expect(dialog.querySelector('[role="status"]')?.textContent).toContain("Failed to load inhabitants");
    const retry = [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Retry")!;
    expect(retry.hidden).toBe(false);
    expect(document.activeElement).toBe(retry);

    nextRoomList = [{ ...initialRows[0], name: "Authoritative after retry" }];
    retry.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("Authoritative after retry"));
    expect(postCount()).toBe(writesBefore + 1);
  });

  it("does not leave a successful PATCH form disabled when its refresh fails", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    roomListResponses.push({
      items: Array.from({ length: 20 }, (_, index) => ({
        ...initialRows[0], id: 501 + index, placement_id: 601 + index, name: `Patch ${String(index).padStart(2, "0")}`,
      })), truncated: true, next_cursor: "cursor-patch-refresh",
    });
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    const card = dialog.querySelector<HTMLElement>('[data-inhabitant-id="501"]')!;
    card.querySelector<HTMLButtonElement>('button')!.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (form.elements.namedItem("name") as HTMLInputElement).value = "Patched before refresh failure";
    const patchCount = () => fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH").length;
    const writesBefore = patchCount();
    failNextRoomList = true;
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-form")).toBeNull());
    expect(patchCount()).toBe(writesBefore + 1);
    expect(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__retry')?.hidden).toBe(false);
    expect(document.activeElement).toBe(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__retry'));
  });

  it("resets to page zero when the old id-only cursor is invalidated", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    const firstPage = Array.from({ length: 20 }, (_, index) => ({
      ...initialRows[0], id: 301 + index, placement_id: 401 + index, name: `Cursor ${String(index).padStart(2, "0")}`,
    }));
    // Raw base64url({"id":20}) — the short-lived cursor emitted before v2.
    roomListResponses.push({ items: firstPage, truncated: true, next_cursor: "eyJpZCI6MjB9" });
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    legacyCursorFailure = true;
    dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__load-more')!.click();
    await vi.waitFor(() => expect(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__retry')?.hidden).toBe(false));
    expect(dialog.querySelector('[role="status"]')?.textContent).toContain("list changed");
    expect(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__previous')?.hidden).toBe(true);
    nextRoomList = [{ ...initialRows[0], name: "Fresh first page" }];
    dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__retry')!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain("Fresh first page"));
    const lastListURL = fetchMock.mock.calls.filter(([url]) => String(url).includes("/inhabitants?")).at(-1)?.[0];
    expect(String(lastListURL)).not.toContain("cursor=");
  });

  it("discards a stale load-more page when live knowledge refresh replaces the room list", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    const firstPage = Array.from({ length: 20 }, (_, index) => ({
      ...initialRows[0], id: 201 + index, placement_id: 301 + index, name: `Page ${String(index).padStart(2, "0")}`,
    }));
    roomListResponses.push({ items: firstPage, truncated: true, next_cursor: "cursor-page" });
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));

    let resolveStale!: (value: Response) => void;
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveStale = resolve; }));
    dialog.querySelector<HTMLButtonElement>('button')?.focus();
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Load more")!.click();
    await vi.waitFor(() => expect(resolveStale).toBeTypeOf("function"));

    roomListResponses.push({ items: [{ ...initialRows[0], id: 999, placement_id: 999, name: "Fresh page" }], truncated: false });
    pane.onRoomKnowledgeChanged(5, "Город", 3, 4, 0);
    await vi.waitFor(() => expect(dialog.textContent).toContain("Fresh page"));
    resolveStale(response({ items: [{ ...initialRows[0], id: 998, placement_id: 998, name: "Stale page" }], truncated: false }));
    await Promise.resolve();
    await Promise.resolve();
    expect(dialog.textContent).not.toContain("Stale page");
    expect(dialog.textContent).toContain("Fresh page");
  });

	it("searches the bounded catalog and places an existing entity", async () => {
	  await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
	  clickRoom();
	  [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
		.find((button) => button.textContent === "Knowledge…")!.click();
	  const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
	  await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
	  [...dialog.querySelectorAll<HTMLButtonElement>("button")]
		.find((button) => button.textContent === "Place existing")!.click();
	  const search = dialog.querySelector<HTMLInputElement>('input[name="catalog_query"]')!;
	  search.value = "лекарь";
	  search.dispatchEvent(new Event("input", { bubbles: true }));
	  await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-catalog__item")?.textContent).toContain("Странствующий лекарь"));
	  const place = [...dialog.querySelectorAll<HTMLButtonElement>("button")]
		.find((button) => button.textContent === "Place")!;
	  expect(place.getAttribute("aria-label")).toContain("Странствующий лекарь");
	  place.click();
	  const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-placement-form")!;
	  (form.elements.namedItem("presence_text") as HTMLTextAreaElement).value = "Лекарь осматривает раненых.";
	  (form.elements.namedItem("resident") as HTMLInputElement).checked = false;
	  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
	  await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-card[data-inhabitant-id=\"8\"]")?.textContent).toContain("Странствующий лекарь"));
	  const placementCall = fetchMock.mock.calls.find(([, init]) => {
		if (init?.method !== "POST") return false;
		return JSON.parse(String(init.body)).inhabitant_id === 8;
	  })!;
	  expect(JSON.parse(String(placementCall[1].body))).toMatchObject({
		inhabitant_id: 8, zone: "Город", x: 3, y: 4, l: 0,
		presence_text: "Лекарь осматривает раненых.", resident: false,
	  });
	  expect(document.activeElement?.closest(".map-inhabitant-card")?.textContent).toContain("Странствующий лекарь");
	});

  it("resets traversal after placing a low-id catalog entry from a later page", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    const firstPage = Array.from({ length: 20 }, (_, index) => ({
      ...initialRows[0], id: 2 + index, placement_id: 202 + index, name: `NPC ${String(2 + index).padStart(2, "0")}`,
    }));
    const secondPage = [{ ...firstPage[0], id: 22, placement_id: 222, name: "NPC 22" }];
    const refreshedFirstPage = Array.from({ length: 20 }, (_, index) => ({
      ...initialRows[0], id: 1 + index, placement_id: 201 + index, name: `NPC ${String(1 + index).padStart(2, "0")}`,
    }));
    const refreshedSecondPage = [
      { ...firstPage[0], id: 21, placement_id: 221, name: "NPC 21" },
      { ...secondPage[0], id: 22, placement_id: 222, name: "NPC 22" },
    ];
    roomListResponses.push(
      { items: firstPage, truncated: true, next_cursor: "cursor-before-placement" },
      { items: secondPage, truncated: false },
      { items: refreshedFirstPage, truncated: true, next_cursor: "cursor-after-placement" },
      { items: refreshedSecondPage, truncated: false },
    );
    catalogItems = [{
      id: 1, map_set_id: 5, name: "Low ID catalog NPC", kind: "npc",
      look_target: "low", strategy: "", placement_count: 0,
    }];
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(20));
    dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__load-more')!.click();
    await vi.waitFor(() => expect(dialog.querySelector('[data-inhabitant-id="22"]')).not.toBeNull());
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Place existing")!.click();
    await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-catalog__item")?.textContent).toContain("Low ID catalog NPC"));
    dialog.querySelector<HTMLButtonElement>('.map-inhabitant-catalog__item button')!.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-placement-form")!;
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(dialog.querySelector('[data-inhabitant-id="1"]')).not.toBeNull());
    const listCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes("/inhabitants?"));
    expect(String(listCalls.at(-1)?.[0])).not.toContain("cursor=");
    expect(dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__previous')?.hidden).toBe(true);
    expect(dialog.querySelector('[data-inhabitant-id="21"]')).toBeNull();
    dialog.querySelector<HTMLButtonElement>('button.map-knowledge-dialog__load-more')!.click();
    await vi.waitFor(() => expect(dialog.querySelector('[data-inhabitant-id="21"]')).not.toBeNull());
    expect(dialog.querySelectorAll(".map-inhabitant-card")).toHaveLength(2);
  });

  it("restores catalog focus on cancel and placement back", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    const catalogOpener = [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Place existing")!;
    catalogOpener.click();
    await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-catalog__item")?.textContent).toContain("Странствующий лекарь"));
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Cancel")!.click();
    expect(document.activeElement).toBe(catalogOpener);

    catalogOpener.click();
    await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-catalog__item")?.textContent).toContain("Странствующий лекарь"));
    const place = [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Place")!;
    place.click();
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Back")!.click();
    expect(document.activeElement).toBe(place);
  });

  it("refreshes only an open matching room on room_knowledge_changed", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    await vi.waitFor(() => expect(pane.root.querySelector(".map-knowledge-dialog")?.textContent).toContain("Кузнец"));
    const knowledgeCalls = () => fetchMock.mock.calls.filter(([url]) => String(url).includes("/inhabitants?")).length;
    const roomCalls = () => fetchMock.mock.calls.filter(([url]) => String(url).includes("/api/rooms?")).length;
    const beforeKnowledge = knowledgeCalls();
    const beforeRooms = roomCalls();

    pane.onRoomKnowledgeChanged(5, "Другая", 3, 4, 0);
    pane.onRoomKnowledgeChanged(4, "Город", 3, 4, 0);
    await Promise.resolve();
    expect(knowledgeCalls()).toBe(beforeKnowledge);

    pane.onRoomKnowledgeChanged(5, "Город", 3, 4, 0);
    await vi.waitFor(() => expect(knowledgeCalls()).toBe(beforeKnowledge + 1));
    expect(roomCalls()).toBe(beforeRooms);
  });

  it("refreshes an open room on a map-set knowledge invalidation", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    await vi.waitFor(() => expect(pane.root.querySelector(".map-knowledge-dialog")?.textContent).toContain("Кузнец"));
    const knowledgeCalls = () => fetchMock.mock.calls.filter(([url]) => {
      const decoded = decodeURIComponent(String(url));
      return decoded.includes("/inhabitants?") && !decoded.includes("/catalog?");
    }).length;
    const before = knowledgeCalls();

    pane.onMapKnowledgeChanged(4);
    await Promise.resolve();
    expect(knowledgeCalls()).toBe(before);
    pane.onMapKnowledgeChanged(5);
    await vi.waitFor(() => expect(knowledgeCalls()).toBe(before + 1));
  });

  it("closes the catalog panel when room knowledge refreshes", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Place existing")!.click();
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-catalog")).toHaveLength(1));
    dialog.querySelector<HTMLInputElement>('input[name="catalog_query"]')!.focus();

    const listCalls = () => fetchMock.mock.calls.filter(([url]) => {
      const decoded = decodeURIComponent(String(url));
      return decoded.includes("/inhabitants?") && !decoded.includes("/catalog?");
    }).length;
    const before = listCalls();
    pane.onRoomKnowledgeChanged(5, "Город", 3, 4, 0);
    await vi.waitFor(() => expect(listCalls()).toBe(before + 1));
    await vi.waitFor(() => expect(dialog.querySelectorAll(".map-inhabitant-catalog")).toHaveLength(0));
    expect(dialog.querySelector<HTMLElement>(".map-knowledge-dialog__list")!.hidden).toBe(false);
    expect(document.activeElement).toBe([...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Place existing"));
  });

  it("preserves a placement form when an invalidation load resolves late and refreshes on Back", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    const catalogOpener = [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Place existing")!;
    catalogOpener.click();
    await vi.waitFor(() => expect(dialog.querySelector(".map-inhabitant-catalog__item")?.textContent).toContain("Странствующий лекарь"));

    let resolveRefresh!: (value: Response) => void;
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveRefresh = resolve; }));
    const listCalls = () => fetchMock.mock.calls.filter(([url]) => {
      const decoded = decodeURIComponent(String(url));
      return decoded.includes("/inhabitants?") && !decoded.includes("/catalog?");
    }).length;
    const before = listCalls();
    pane.onRoomKnowledgeChanged(5, "Город", 3, 4, 0);
    await vi.waitFor(() => expect(listCalls()).toBe(before + 1));

    const place = dialog.querySelector<HTMLButtonElement>('.map-inhabitant-catalog__item button')!;
    place.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-placement-form")!;
    (form.elements.namedItem("presence_text") as HTMLTextAreaElement).value = "Несохранённое размещение";
    resolveRefresh(response(initialRows.map((row) => ({ ...row }))));
    await Promise.resolve();
    await Promise.resolve();
    expect(dialog.querySelector(".map-inhabitant-placement-form")).toBe(form);
    expect((form.elements.namedItem("presence_text") as HTMLTextAreaElement).value).toBe("Несохранённое размещение");

    [...form.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Back")!.click();
    await vi.waitFor(() => expect(listCalls()).toBe(before + 2));
    expect(dialog.querySelector(".map-inhabitant-placement-form")).toBeNull();
    expect(dialog.querySelector(".map-inhabitant-catalog")).toBeNull();
    expect(document.activeElement).toBe(catalogOpener);
  });

  it("applies input limits and blocks a missing name before REST validation", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    await vi.waitFor(() => expect(pane.root.querySelector(".map-knowledge-dialog")?.textContent).toContain("Кузнец"));
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Add new")!.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    const name = form.elements.namedItem("name") as HTMLInputElement;
    const note = form.elements.namedItem("note") as HTMLTextAreaElement;
    expect(name.required).toBe(true);
    expect(name.maxLength).toBe(256);
    expect(note.maxLength).toBe(2048);

    const writesBefore = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST").length;
    name.value = "";
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await Promise.resolve();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(writesBefore);
  });

  it("keeps an unsaved add form when the initial GET resolves late", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    let resolveLoad!: (value: Response) => void;
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveLoad = resolve; }));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Add new")!.click();
    const form = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    const name = form.elements.namedItem("name") as HTMLInputElement;
    name.value = "Несохранённый NPC";

    resolveLoad(response(initialRows));
    await Promise.resolve();
    await Promise.resolve();
    expect(dialog.querySelector(".map-inhabitant-form")).toBe(form);
    expect((form.elements.namedItem("name") as HTMLInputElement).value).toBe("Несохранённый NPC");
  });

  it("switches an open Knowledge form to Russian without losing unsaved or MUD text", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Knowledge…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    expect(dialog.querySelector('.map-knowledge-dialog__room')?.textContent).toContain('Кузница');
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Add new")!.click();
    const original = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    (original.elements.namedItem("name") as HTMLInputElement).value = "Несохранённый NPC";
    (original.elements.namedItem("presence_text") as HTMLTextAreaElement).value = "Сырая строка MUD";

    pane.setLocale("ru");
    const translated = dialog.querySelector<HTMLFormElement>(".map-inhabitant-form")!;
    expect(dialog.getAttribute("aria-label")).toBe("Знания");
    expect(dialog.textContent).toContain("Добавить обитателя");
    expect(dialog.querySelector('.map-knowledge-dialog__room')?.textContent).toContain('Кузница');
    expect((translated.elements.namedItem("name") as HTMLInputElement).value).toBe("Несохранённый NPC");
    expect((translated.elements.namedItem("presence_text") as HTMLTextAreaElement).value).toBe("Сырая строка MUD");

    pane.setLocale("unsupported");
    expect(dialog.getAttribute("aria-label")).toBe("Знания");
    expect((translated.elements.namedItem("name") as HTMLInputElement).value).toBe("Несохранённый NPC");
  });

  it('recomputes an unnamed room fallback across EN → RU → EN', async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe('Город'));
    clickRoom({ ...room, h: '' });
    [...pane.root.querySelectorAll<HTMLButtonElement>('.map-cell-menu__item')]
      .find((button) => button.textContent === 'Knowledge…')!.click();
    const roomLine = pane.root.querySelector<HTMLElement>('.map-knowledge-dialog__room')!;
    expect(roomLine.textContent).toBe('Room · Город (3,4,0)');

    pane.setLocale('ru');
    expect(roomLine.textContent).toBe('Комната · Город (3,4,0)');
    pane.setLocale('en');
    expect(roomLine.textContent).toBe('Room · Город (3,4,0)');
  });

  it("localizes a newly opened Russian list and catalog without translating NPC data or kind values", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    pane.setLocale('ru');
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Знания…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    expect(dialog.textContent).toContain('Присутствие: Кузнец у наковальни.');
    expect(dialog.textContent).toContain('npc');
    expect(dialog.textContent).toContain('Седой мастер.');
    [...dialog.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent === 'Разместить существующего')!.click();
    await vi.waitFor(() => expect(dialog.textContent).toContain('Странствующий лекарь · npc · 1 размещение'));
    const place = dialog.querySelector<HTMLButtonElement>('.map-inhabitant-catalog__item button')!;
    expect(place.textContent).toBe('Разместить');
    expect(place.getAttribute('aria-label')).toBe('Разместить: Странствующий лекарь');
  });
});
