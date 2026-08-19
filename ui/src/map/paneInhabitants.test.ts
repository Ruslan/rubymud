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

  beforeEach(() => {
    stubCanvas();
    vi.spyOn(window, "confirm").mockReturnValue(true);
    fetchMock.mockImplementation((rawURL: string, init?: RequestInit) => {
      const url = decodeURIComponent(rawURL);
      if (url.includes("map-live-room")) return Promise.resolve(response({ seen: false }));
      if (url.endsWith("/api/map-sets/5/zones")) return Promise.resolve(response([{ zone: "Город", room_count: 1 }]));
      if (url.includes("/api/rooms?map_set=5&zone=Город")) return Promise.resolve(response([room]));
      if (url.includes("/api/map-sets/5/inhabitants") && (!init?.method || init.method === "GET")) {
		if (url.includes("/catalog?")) {
		  return Promise.resolve(response({ items: [{
			id: 8, map_set_id: 5, name: "Странствующий лекарь", kind: "npc",
			look_target: "лекарь", strategy: "Купить бинты.", placement_count: 1,
		  }], truncated: false }));
		}
        return Promise.resolve(response(initialRows.map((row) => ({ ...row }))));
      }
      if (url.endsWith("/api/map-sets/5/inhabitants") && init?.method === "POST") {
        const body = JSON.parse(String(init.body));
		if (body.inhabitant_id === 8) {
		  return Promise.resolve(response({
			id: 8, placement_id: 18, map_set_id: 5, name: "Странствующий лекарь", kind: "npc",
			look_target: "лекарь", look_text: "", note: "", strategy: "Купить бинты.", ...body,
		  }, 201));
		}
        return Promise.resolve(response({ id: 3, placement_id: 13, map_set_id: 5, ...body }, 201));
      }
      if (url.endsWith("/api/map-sets/5/inhabitants/1") && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        return Promise.resolve(response({ id: 1, placement_id: 11, map_set_id: 5, ...body }));
      }
      if (url.includes("/api/map-sets/5/inhabitants/2?") && init?.method === "DELETE") {
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

  function clickRoom(): void {
    const renderer = (pane as unknown as {
      renderer: { onRoomClick?: (room: SlimRoom, x: number, y: number) => void };
    }).renderer;
    renderer.onRoomClick?.(room, 100, 120);
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
});
