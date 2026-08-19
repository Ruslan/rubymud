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
        return Promise.resolve(response(initialRows));
      }
      if (url.endsWith("/api/map-sets/5/inhabitants") && init?.method === "POST") {
        const body = JSON.parse(String(init.body));
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
      .find((button) => button.textContent === "Знания…")!;
    knowledge.click();

    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    expect(dialog.getAttribute("aria-label")).toBe("Знания");
    const tab = dialog.querySelector<HTMLElement>('[role="tab"]')!;
    const panel = dialog.querySelector<HTMLElement>('[role="tabpanel"]')!;
    expect(tab.textContent).toBe("Обитатели");
    expect(tab.getAttribute("aria-controls")).toBe(panel.id);
    await vi.waitFor(() => expect(dialog.textContent).toContain("Кузнец"));
    expect(dialog.textContent).toContain("Крыса");
    const listCall = fetchMock.mock.calls.find(([url]) => String(url).includes("/inhabitants?"))!;
    expect(decodeURIComponent(String(listCall[0]))).toContain("zone=Город&x=3&y=4&l=0");

    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Добавить")!.click();
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
      .find((button) => button.textContent === "Изменить")!.click();
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
      .find((button) => button.textContent === "Удалить")!.click();
    await vi.waitFor(() => expect(dialog.textContent).not.toContain("Крыса"));
    const deleteCall = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE")!;
    expect(decodeURIComponent(String(deleteCall[0]))).toContain("/inhabitants/2?zone=Город&x=3&y=4&l=0");

    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Закрыть")!.click();
    await Promise.resolve();
    expect(pane.root.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
  });

  it("refreshes only an open matching room on room_knowledge_changed", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Знания…")!.click();
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

  it("applies input limits and blocks a missing name before REST validation", async () => {
    await vi.waitFor(() => expect((pane as unknown as { currentZone: string }).currentZone).toBe("Город"));
    clickRoom();
    [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__item")]
      .find((button) => button.textContent === "Знания…")!.click();
    await vi.waitFor(() => expect(pane.root.querySelector(".map-knowledge-dialog")?.textContent).toContain("Кузнец"));
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Добавить")!.click();
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
      .find((button) => button.textContent === "Знания…")!.click();
    const dialog = pane.root.querySelector<HTMLElement>(".map-knowledge-dialog")!;
    [...dialog.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "Добавить")!.click();
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
