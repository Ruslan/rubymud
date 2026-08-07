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

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
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
  z: "Город", t: 1, h: "Восточные ворота",
  x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [],
  p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0,
  m: [], mb: [], g: [], gf: [],
};

describe("map pane seam chooser", () => {
  let pane: MapPaneController;

  beforeEach(() => {
    stubCanvas();
    fetchMock.mockReset();
    fetchMock.mockResolvedValue(jsonRes({ seen: false }));
    pane = new MapPaneController({
      sessionID: 7,
      getActiveMapSetID: () => null,
    });
    document.body.appendChild(pane.root);
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    vi.restoreAllMocks();
  });

  function clickRoom(value: SlimRoom): void {
    const renderer = (pane as unknown as {
      renderer: {
        onRoomClick?: (
          room: SlimRoom,
          clientX: number,
          clientY: number,
        ) => void;
      };
    }).renderer;
    renderer.onRoomClick?.(value, 100, 120);
  }

  function seamRoom(entries: Array<[string, string, number]>, attrs: Partial<SlimRoom["se"][number]> = {}): SlimRoom {
    return { ...room, se: entries.map(([tz, c, tt], o) => ({
      o, k: "seam", r: "parsed", p: true, tz, tt, c,
      z: tz, x: o + 1, y: o + 2, l: 0,
      ...attrs,
    })) };
  }

  it("renders one seam as a direct amber menu button with ordinary room actions", () => {
    const jumpSeam = vi.spyOn(
      pane as unknown as {
        jumpSeam: (seam: unknown, room: SlimRoom) => Promise<void>;
      },
      "jumpSeam",
    );
    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));

    const menu = pane.root.querySelector(".map-cell-menu");
    const seam = menu?.querySelector<HTMLButtonElement>(".map-cell-menu__seam-item");
    expect(menu?.querySelector(".map-cell-menu__seams-toggle")).toBeNull();
    expect(menu?.textContent).not.toContain("Переходы между зонами");
    expect(seam?.textContent).toBe("на восток → Восточная дорога");
    expect(seam?.title).toBe("Целевая метка: 41");
    expect(seam?.classList.contains("map-cell-menu__seam-item")).toBe(true);
    expect(jumpSeam).not.toHaveBeenCalled();

    const labels = [...menu!.querySelectorAll<HTMLButtonElement>(
      ".map-cell-menu__item",
    )].map((button) => button.textContent);
    expect(labels).toContain("Обновить комнату из живого");
    expect(labels).toContain("Вставить путь в чат");
    expect(labels).toContain("Начать маршрут");
    expect(labels).toContain("Я здесь");
    expect(labels).toContain("Отменить последнее изменение карты");
  });

  it("renders every valid seam directly and selects the seam the user clicked", () => {
    const jumpSeam = vi.spyOn(
      pane as unknown as {
        jumpSeam: (seam: unknown, room: SlimRoom) => Promise<void>;
      },
      "jumpSeam",
    );
    const value = seamRoom([
      ["Восточная дорога", "на восток", 41],
      ["Северная дорога", "на север", 42],
    ]);
    clickRoom(value);

    const labels = [...pane.root.querySelectorAll<HTMLButtonElement>(
      ".map-cell-menu__seam-item",
    )].map((button) => button.textContent);
    expect(labels).toEqual([
      "на восток → Восточная дорога",
      "на север → Северная дорога",
    ]);
    expect(pane.root.querySelectorAll(".map-cell-menu__seam-item")).toHaveLength(2);
    pane.root
      .querySelectorAll<HTMLButtonElement>(".map-cell-menu__seam-item")[1]
      ?.click();
    expect(jumpSeam).toHaveBeenCalledWith(
      { zone: "Северная дорога", command: "на север", tag: 42, source: "parsed", passable: true, order: 1,
        to: { zone: "Северная дорога", x: 2, y: 3, l: 0 } },
      value,
    );
  });

  it("keeps navigation and properties as independent accessible controls", () => {
    const jumpSeam = vi.spyOn(
      pane as unknown as { jumpSeam: (seam: unknown, room: SlimRoom) => Promise<void> },
      "jumpSeam",
    );
    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));

    const nav = pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-item")!;
    const props = pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!;
    expect(props.getAttribute("aria-label")).toBe(
      "Свойства перехода на восток в Восточная дорога",
    );
    props.focus();
    expect(document.activeElement).toBe(props);
    props.click();
    expect(jumpSeam).not.toHaveBeenCalled();
    expect(pane.root.querySelector(".map-edge-menu")?.textContent).toContain(
      "Переход: на восток → Восточная дорога",
    );

    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-item")!.click();
    expect(jumpSeam).toHaveBeenCalledTimes(1);
    expect(nav.disabled).toBe(false);
  });

  it("opens the selected seam attrs and posts its exact semantic selector", async () => {
    const value: SlimRoom = {
      ...seamRoom([["Восточная дорога", "на восток", 41], ["Северная дорога", "teleport", 42]]),
      se: seamRoom([["Восточная дорога", "на восток", 41], ["Северная дорога", "teleport", 42]]).se.map((s) => s.tt === 42 ? { ...s, weight: 2.5, phrase: "enter portal" } : s),
    };
    clickRoom(value);
    pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__seam-props")[1]!.click();

    const menu = pane.root.querySelector<HTMLElement>(".map-edge-menu")!;
    const inputs = menu.querySelectorAll<HTMLInputElement>("input");
    expect(inputs[0]!.value).toBe("2.5");
    expect(inputs[1]!.value).toBe("enter portal");
    inputs[0]!.value = "3";
    inputs[0]!.dispatchEvent(new Event("blur"));
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) =>
      url === "/api/sessions/7/map-edge-attrs",
    )).toBe(true));

    const [url, init] = fetchMock.mock.calls.find(([calledURL]) =>
      calledURL === "/api/sessions/7/map-edge-attrs",
    ) as [string, RequestInit];
    expect(url).toBe("/api/sessions/7/map-edge-attrs");
    expect(JSON.parse(String(init.body))).toEqual({
      zone: "Город", x: 0, y: 0, l: 0,
      seam: { target_zone: "Северная дорога", target_tag: 42, command: "teleport", order: 1 },
      weight: 3,
    });
  });

  it("writes exact-portal attributes through the occurrence endpoint", async () => {
    const value: SlimRoom = {
      ...room,
      se: [{
        o: 7, k: "seam", r: "manual", p: true, tz: "Другая зона", c: "enter",
        z: "Другая зона", x: 4, y: 5, l: 0, weight: 2,
      }],
    };
    clickRoom(value);
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__portal-repair")!.click();

    const weight = pane.root.querySelector<HTMLInputElement>('[data-map-edge-attr="weight"]')!;
    weight.value = "3";
    weight.dispatchEvent(new FocusEvent("blur"));
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) =>
      url === "/api/sessions/7/map-portals",
    )).toBe(true));

    const [, init] = fetchMock.mock.calls.find(([url]) =>
      url === "/api/sessions/7/map-portals",
    ) as [string, RequestInit];
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(String(init.body))).toEqual({
      zone: "Город", x: 0, y: 0, l: 0, order: 7, weight: 3,
    });
  });

  it("keeps only the latest target-zone response in the portal picker", async () => {
    const b = deferred<Response>();
    const c = deferred<Response>();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes("zone=B")) return b.promise;
      if (url.includes("zone=C")) return c.promise;
      if (url.includes("zone=A")) return Promise.resolve(jsonRes([{ ...room, z: "A", h: "A room" }]));
      return Promise.resolve(jsonRes({ seen: false }));
    });
    const internal = pane as unknown as {
      mapSetID: number;
      zoneNames: string[];
      loadGeneration: number;
    };
    internal.mapSetID = 1;
    internal.zoneNames = ["A", "B", "C"];
    clickRoom({ ...room, z: "A" });
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__portal-add")!.click();

    const selects = pane.root.querySelectorAll<HTMLSelectElement>(".map-edge-menu__portal-details select");
    const zone = selects[0]!;
    const target = selects[1]!;
    zone.value = "B";
    zone.dispatchEvent(new Event("change"));
    zone.value = "C";
    zone.dispatchEvent(new Event("change"));
    const bCall = fetchMock.mock.calls.find(([url]) => String(url).includes("zone=B")) as [string, RequestInit];
    expect((bCall[1].signal as AbortSignal).aborted).toBe(true);
    c.resolve(jsonRes([
      { ...room, z: "C", h: "C newest", x: 3 },
      { ...room, z: "C", h: "C other", x: 9 },
    ]));
    await vi.waitFor(() => expect(target.textContent).toContain("C newest"));
    b.resolve(jsonRes([{ ...room, z: "B", h: "B stale", x: 2 }]));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(target.textContent).toContain("C newest");
    expect(target.textContent).not.toContain("B stale");
    const search = pane.root.querySelector<HTMLInputElement>('.map-edge-menu__portal-details input[type="search"]')!;
    search.value = "3,0,0";
    search.dispatchEvent(new Event("input"));
    expect(target.options).toHaveLength(1);
    expect(target.textContent).toContain("C newest");
  });

  it("does not let an old portal response close a reopened editor", async () => {
    const first = deferred<Response>();
    fetchMock.mockImplementation((url: string) =>
      url.includes("map-portals") ? first.promise : Promise.resolve(jsonRes({ seen: false })),
    );
    const internal = pane as unknown as {
      writePortal(room: SlimRoom, seam: null, value: { command: string; target: { zone: string; x: number; y: number; l: number } }): Promise<void>;
    };
    const pending = internal.writePortal(room, null, {
      command: "first", target: { zone: "Город", x: 0, y: 0, l: 0 },
    });
    clickRoom(room);
    expect(pane.root.querySelector(".map-cell-menu")).not.toBeNull();

    first.resolve(jsonRes({ ok: true }));
    await pending;
    expect(pane.root.querySelector(".map-cell-menu")).not.toBeNull();
  });

  it("keeps seam properties open so a new door can receive an opening command", async () => {
    const doorWrite = deferred<Response>();
    fetchMock.mockImplementation((url: string) =>
      url.includes("map-edge-attrs") ? doorWrite.promise : Promise.resolve(jsonRes({ seen: false })),
    );
    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();

    const door = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!;
    door.click();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/sessions/7/map-edge-attrs",
      expect.objectContaining({ method: "POST" }),
    ));

    expect(pane.root.querySelector(".map-edge-menu")).not.toBeNull();
    const pendingDoor = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!;
    expect(pendingDoor.checked).toBe(true);
    expect(pendingDoor.disabled).toBe(true);
    expect([...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("заперта")))
      .toBeDefined();
    expect([...pane.root.querySelectorAll<HTMLInputElement>('input[type="text"]')]
      .find((input) => input.parentElement?.textContent?.includes("команда открытия")))
      .toBeDefined();

    doorWrite.resolve(jsonRes({ ok: true }));
    const openCommand = await vi.waitFor(() => {
      const input = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="text"]')]
        .find((candidate) => candidate.parentElement?.textContent?.includes("команда открытия"));
      expect(input?.disabled).toBe(false);
      return input!;
    });
    openCommand.value = "open east gate";
    openCommand.dispatchEvent(new FocusEvent("blur"));
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([url]) =>
      url.includes("map-edge-attrs"),
    )).toHaveLength(2));

    const writes = fetchMock.mock.calls.filter(([url]) => url.includes("map-edge-attrs"));
    expect(JSON.parse(String(writes[0]![1]!.body))).toEqual({
      zone: "Город", x: 0, y: 0, l: 0,
      seam: { target_zone: "Восточная дорога", target_tag: 41, command: "на восток", order: 0 },
      door: true,
    });
    expect(JSON.parse(String(writes[1]![1]!.body))).toEqual({
      zone: "Город", x: 0, y: 0, l: 0,
      seam: { target_zone: "Восточная дорога", target_tag: 41, command: "на восток", order: 0 },
      open_cmd: "open east gate",
    });
  });

  it("keeps keyboard focus and reclamps the popup when optimistic door fields expand", async () => {
    const doorWrite = deferred<Response>();
    fetchMock.mockImplementation((url: string) =>
      url.includes("map-edge-attrs") ? doorWrite.promise : Promise.resolve(jsonRes({ seen: false })),
    );
    const rect = (width: number, height: number) => ({
      left: 0, top: 0, width, height, right: width, bottom: height,
      x: 0, y: 0, toJSON() {},
    }) as DOMRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function () {
      if (this === pane.root) return rect(240, 120);
      if (this.classList.contains("map-cell-menu__seam-props")) {
        return { ...rect(20, 10), top: 90, bottom: 100, y: 90 } as DOMRect;
      }
      if (this.classList.contains("map-edge-menu")) {
        const hasOpen = [...this.querySelectorAll<HTMLInputElement>('input[type="text"]')]
          .some((input) => input.parentElement?.textContent?.includes("команда открытия"));
        return rect(180, hasOpen ? 140 : 60);
      }
      return rect(0, 0);
    });
    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();

    const menu = pane.root.querySelector<HTMLElement>(".map-edge-menu")!;
    expect(menu.style.top).toBe("40px");
    const door = [...menu.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!;
    door.focus();
    door.click();
    expect(menu.style.top).toBe("6px");

    doorWrite.resolve(jsonRes({ ok: true }));
    await vi.waitFor(() => {
      const currentDoor = [...menu.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
        .find((input) => input.parentElement?.textContent?.includes("дверь"));
      expect(currentDoor?.disabled).toBe(false);
      expect(document.activeElement).toBe(currentDoor);
    });
  });

  it("optimistically hides door-only fields when a seam door is removed", async () => {
    const doorWrite = deferred<Response>();
    fetchMock.mockImplementation((url: string) =>
      url.includes("map-edge-attrs") ? doorWrite.promise : Promise.resolve(jsonRes({ seen: false })),
    );
    const value: SlimRoom = {
      ...seamRoom([["Восточная дорога", "на восток", 41]], { door: true, locked: true, open_cmd: "open east" }),
    };
    clickRoom(value);
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();

    [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!
      .click();
    expect([...pane.root.querySelectorAll<HTMLInputElement>('input[type="text"]')]
      .find((input) => input.parentElement?.textContent?.includes("команда открытия")))
      .toBeUndefined();
    doorWrite.resolve(jsonRes({ ok: true }));
    await vi.waitFor(() => expect(JSON.parse(String(fetchMock.mock.calls
      .find(([url]) => url.includes("map-edge-attrs"))![1]!.body)).door).toBe(false));
  });

  it("rolls a network exception back without closing the seam editor", async () => {
    fetchMock.mockImplementation((url: string) => url.includes("map-edge-attrs")
      ? Promise.reject(new Error("offline"))
      : Promise.resolve(jsonRes({ seen: false })));
    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();

    [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!
      .click();

    await vi.waitFor(() => {
      expect([...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
        .find((input) => input.parentElement?.textContent?.includes("дверь"))?.checked)
        .toBe(false);
      expect(pane.root.querySelector(".map-edge-menu")).not.toBeNull();
    });
  });

  it("serializes writes across a closed and reopened editor and keeps the latest status", async () => {
    const first = deferred<Response>();
    const second = deferred<Response>();
    let writes = 0;
    fetchMock.mockImplementation((url: string) => {
      if (!url.includes("map-edge-attrs")) return Promise.resolve(jsonRes({ seen: false }));
      writes++;
      return writes === 1 ? first.promise : second.promise;
    });
    const value = seamRoom([["Восточная дорога", "на восток", 41]]);
    clickRoom(value);
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();
    [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!
      .click();

    clickRoom(value);
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();
    [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!
      .click();
    await Promise.resolve();
    expect(fetchMock.mock.calls.filter(([url]) => url.includes("map-edge-attrs"))).toHaveLength(1);

    first.resolve(jsonRes({ ok: false, reason: "old failure" }));
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([url]) =>
      url.includes("map-edge-attrs"),
    )).toHaveLength(2));
    second.resolve(jsonRes({ ok: true }));

    await vi.waitFor(() => {
      const door = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
        .find((input) => input.parentElement?.textContent?.includes("дверь"));
      expect(door?.checked).toBe(true);
      expect(door?.disabled).toBe(false);
    });
    expect((pane as unknown as { statusText: HTMLElement }).statusText.textContent).not.toContain("old failure");
  });

  it("rolls a failed seam door edit back in the open properties menu", async () => {
    const doorWrite = deferred<Response>();
    fetchMock.mockImplementation((url: string) =>
      url.includes("map-edge-attrs") ? doorWrite.promise : Promise.resolve(jsonRes({ seen: false })),
    );
    clickRoom(seamRoom([["Восточная дорога", "на восток", 41]]));
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();

    [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("дверь"))!
      .click();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/sessions/7/map-edge-attrs",
      expect.anything(),
    ));
    doorWrite.resolve(jsonRes({ ok: false, reason: "denied" }));

    await vi.waitFor(() => {
      const door = [...pane.root.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
        .find((input) => input.parentElement?.textContent?.includes("дверь"));
      expect(door?.checked).toBe(false);
      expect([...pane.root.querySelectorAll<HTMLInputElement>('input[type="text"]')]
        .find((input) => input.parentElement?.textContent?.includes("команда открытия")))
        .toBeUndefined();
    });
    expect(pane.root.querySelector(".map-edge-menu")).not.toBeNull();
  });

  it("uses a parseable seam direction for the default door command", () => {
    (pane as unknown as { doorOpen: string }).doorOpen = "open {dir_en}";
    const value: SlimRoom = {
      ...seamRoom([["Восточная дорога", "на восток", 41]], { door: true }),
    };
    clickRoom(value);
    pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.click();

    const menu = pane.root.querySelector<HTMLElement>(".map-edge-menu")!;
    expect(menu.textContent).toContain("перед шагом маршрут пошлёт «open e»");
    const open = [...menu.querySelectorAll<HTMLInputElement>('input[type="text"]')]
      .find((input) => input.parentElement?.textContent?.includes("команда открытия"));
    expect(open?.placeholder).toBe("open e");
  });

  it("renders dead normalized seams without hiding valid ones", () => {
    clickRoom(
      { ...room, se: [
        { o: 0, k: "seam", r: "parsed", p: true, tz: "broken", c: "" },
        { o: 1, k: "seam", r: "parsed", p: true, tz: "Южная дорога", tt: 55, c: "на юг", z: "Южная дорога", x: 2, y: 3, l: 0 },
      ] },
    );
    const labels = [...pane.root.querySelectorAll<HTMLButtonElement>(
      ".map-cell-menu__seam-item",
    )].map((button) => button.textContent);
    expect(labels).toEqual(["переход → broken", "на юг → Южная дорога"]);
    const nav = [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__seam-item")];
    const props = [...pane.root.querySelectorAll<HTMLButtonElement>(".map-cell-menu__seam-props")];
    expect(nav[0]!.disabled).toBe(true);
    expect(nav[1]!.disabled).toBe(false);
    expect(props[0]!.disabled).toBe(true);
    expect(props[1]!.disabled).toBe(false);
  });

  it("keeps a blocked resolved seam visible and editable but not navigable", () => {
    clickRoom(seamRoom([["Закрытый портал", "войти", 9]], { p: false }));
    expect(pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-item")!.disabled).toBe(true);
    expect(pane.root.querySelector<HTMLButtonElement>(".map-cell-menu__seam-props")!.disabled).toBe(false);
  });

  it("does not render seam controls for an ordinary room", () => {
    clickRoom(room);
    expect(pane.root.querySelector(".map-cell-menu__seams-toggle")).toBeNull();
  });

  it("keeps a long direct seam list inside the scrolling menu contract", () => {
    pane.root.getBoundingClientRect = () =>
      ({
        left: 0, top: 0, width: 240, height: 120, right: 240, bottom: 120,
        x: 0, y: 0, toJSON() {},
      }) as DOMRect;
    clickRoom(
      seamRoom([["A", "a", 1], ["B", "b", 2], ["C", "c", 3], ["D", "d", 4], ["E", "e", 5]]),
    );
    const menu = pane.root.querySelector<HTMLElement>(".map-cell-menu")!;
    expect(menu.style.maxHeight).toBe("108px");
    expect(menu.style.maxHeight).toBe("108px");
    expect(menu.querySelectorAll(".map-cell-menu__seam-item")).toHaveLength(5);
    expect(menu.querySelectorAll(".map-cell-menu__seam-props")).toHaveLength(5);
    expect(menu.classList.contains("map-cell-menu")).toBe(true);
  });

  it("keeps direct seam buttons in the tab order", () => {
    clickRoom(seamRoom([["A", "a", 1], ["B", "b", 2]]));
    const seam = pane.root.querySelector<HTMLButtonElement>(
      ".map-cell-menu__seam-item",
    )!;

    seam.focus();

    expect(document.activeElement).toBe(seam);
    expect(seam.disabled).toBe(false);
  });

  it("wraps an unbroken seam label within a narrow menu row", () => {
    pane.root.style.width = "180px";
    clickRoom(seamRoom([["ОченьДлиннаяЦельБезПробелов", "командабезпробелов", 9]]));
    const seam = pane.root.querySelector<HTMLButtonElement>(
      ".map-cell-menu__seam-item",
    )!;

    expect(seam.textContent).toContain("командабезпробелов → ОченьДлиннаяЦельБезПробелов");
    expect(seam.classList.contains("map-cell-menu__seam-item")).toBe(true);
  });
});
