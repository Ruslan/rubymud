import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { MapRenderer, pipeAxis } from "./renderer";
import type { SlimRoom } from "./types";

// jsdom's <canvas>.getContext('2d') returns null, so to exercise MapRenderer's
// listener lifecycle we hand it a canvas whose getContext returns a no-op 2d
// context stub. Only the members the renderer actually calls are stubbed.
function fakeCtx(): CanvasRenderingContext2D {
  const noop = () => {};
  return new Proxy(
    {
      setTransform: noop,
      clearRect: noop,
      beginPath: noop,
      moveTo: noop,
      lineTo: noop,
      arc: noop,
      arcTo: noop,
      closePath: noop,
      fill: noop,
      stroke: noop,
      setLineDash: noop,
      fillText: noop,
      fillStyle: "",
      strokeStyle: "",
      lineWidth: 0,
      font: "",
      textAlign: "",
      textBaseline: "",
    },
    { get: (t, p) => (p in t ? (t as any)[p] : noop) },
  ) as unknown as CanvasRenderingContext2D;
}

function makeCanvas(): HTMLCanvasElement {
  const cv = document.createElement("canvas");
  const ctx = fakeCtx();
  vi.spyOn(cv, "getContext").mockReturnValue(ctx as any);
  // clientWidth/Height are 0 in jsdom; give the renderer a viewport to work with.
  Object.defineProperty(cv, "clientWidth", { value: 600, configurable: true });
  Object.defineProperty(cv, "clientHeight", { value: 400, configurable: true });
  cv.getBoundingClientRect = () => ({
    left: 0,
    top: 0,
    width: 600,
    height: 400,
    right: 600,
    bottom: 400,
    x: 0,
    y: 0,
    toJSON() {},
  });
  return cv;
}

function slim(over: Partial<SlimRoom> = {}): SlimRoom {
  return {
    z: "Z", t: null, h: "", x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [],
    p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0,
    m: [], mb: [], g: [], gf: [], ...over,
  };
}

describe("pipe orientation", () => {

  it("uses presentation coordinates for drawing and hit-testing while retaining logical identity", () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    const room = slim({ x: 1, y: 2, l: 0, px: 8, py: 9, pl: 3 });
    r.setZone("Z", [room]);
    r.centerOn(1, 2, 0);
    expect(r.level).toBe(3);
    expect(r.pick(300, 200)).toBe(room);
    expect(r.findRoom(1, 2, 0)).toBe(room);
  });

  it("projects a logical player marker onto the room presentation position and floor", () => {
    const cv = makeCanvas();
    const ctx = fakeCtx();
    vi.mocked(cv.getContext).mockReturnValue(ctx as any);
    const arc = vi.spyOn(ctx, "arc");
    const r = new MapRenderer(cv);
    r.setZone("Z", [slim({ x: 1, y: 2, px: 8, py: 9, pl: 3 })]);
    r.setMarker({ x: 1, y: 2, l: 0, color: "green" });
    r.centerOn(1, 2, 0);
    (r as unknown as { paint(): void }).paint();
    expect(arc.mock.calls.some(([x, y]) => x === 300 && y === 200)).toBe(true);
  });

  it("paints an exact same-zone corridor as one clipped display primitive", () => {
    const cv = makeCanvas();
    const ctx = cv.getContext("2d")!;
    const moveTo = vi.spyOn(ctx, "moveTo");
    const lineTo = vi.spyOn(ctx, "lineTo");
    const stroke = vi.spyOn(ctx, "stroke");
    const from = slim({ x: 1, y: 1, px: 0, py: 0, co: [{ dir: "N", r: "reconciled", p: true, z: "Z", x: 0, y: 0, l: 0, c: "climb rope" }] });
    const to = slim({ x: 0, y: 0, px: 0, py: 4 });
    const r = new MapRenderer(cv);
    r.setZone("Z", [from, to]);
    r.centerOn(1, 1, 0);
    (r as unknown as { paint(): void }).paint();
    expect(stroke).toHaveBeenCalledTimes(1);
    const [ax, ay] = moveTo.mock.calls[0]!;
    const [bx, by] = lineTo.mock.calls[0]!;
    expect(ay).toBe(by);
    expect(Number(bx) - Number(ax)).toBeGreaterThan(100);
    expect(ax).not.toBe(300); // clipped at the source room edge
  });

  it("paints the relaid 4-5 grid passage once as one long primitive", () => {
    const cv = makeCanvas(); const ctx = cv.getContext("2d")!;
    const stroke = vi.spyOn(ctx,"stroke"); const moveTo=vi.spyOn(ctx,"moveTo"); const lineTo=vi.spyOn(ctx,"lineTo");
    // Logical neighbours 4(1,2) W 5(1,1); display 5 moved under room 1 to y=0.
    const four=slim({h:"4",x:1,y:2,g:["W"]});
    const five=slim({h:"5",x:1,y:1,py:0,g:["E"]});
    const r=new MapRenderer(cv);r.setZone("Z",[four,five]);r.centerOn(1,2,0);(r as unknown as {paint():void}).paint();
    expect(stroke).toHaveBeenCalledTimes(1);
    const [ax,ay]=moveTo.mock.calls[0]!;const [bx,by]=lineTo.mock.calls[0]!;
    expect(ay).toBe(by);expect(Math.abs(Number(bx)-Number(ax))).toBeGreaterThan(30);
  });

  it("paints an adjacent materialized passage omitted from both exit lines as dashed", () => {
    const cv = makeCanvas();
    const ctx = cv.getContext("2d")!;
    let dashed = false;
    let dashedStrokes = 0;
    const stroke = vi.spyOn(ctx, "stroke").mockImplementation(() => { if (dashed) dashedStrokes++; });
    const setLineDash = vi.spyOn(ctx, "setLineDash").mockImplementation((dash) => { dashed = dash.length > 0; });
    const west = slim({ h: "Беседка", x: 0, y: -1, e: [], g: ["E"] });
    const east = slim({ h: "Поворот", x: 0, y: 0, e: ["N", "S"], g: ["N", "S", "W"] });
    const r = new MapRenderer(cv);
    r.setZone("Z", [west, east]);
    r.centerOn(0, 0, 0);

    (r as unknown as { paint(): void }).paint();

    expect(stroke).toHaveBeenCalledTimes(4); // two ordinary exits plus two hidden halves
    expect(dashedStrokes).toBe(2);
    expect(setLineDash.mock.calls.some(([dash]) => dash.length > 0)).toBe(true);
  });
  it("uses N/S connectivity before four-neighbour geometry", () => {
    expect(pipeAxis(3, { hL: true, hR: true, vU: true, vD: true })).toBe("v");
  });

  it("uses E/W connectivity before four-neighbour geometry", () => {
    expect(pipeAxis(12, { hL: true, hR: true, vU: true, vD: true })).toBe("h");
  });

  it("falls back to neighbour geometry for empty or ambiguous connectivity", () => {
    expect(pipeAxis(0, { hL: false, hR: false, vU: true, vD: true })).toBe("v");
    expect(pipeAxis(15, { hL: true, hR: true, vU: false, vD: false })).toBe("h");
  });

  it("draws an N/S pipe as a tall narrow bar despite side neighbours", () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    const roundRect = vi.spyOn(
      r as unknown as { roundRect: () => void },
      "roundRect",
    );
    r.setZone("Z", [
      slim({ x: 9, y: -6, p: true, ch: 3 }),
      slim({ x: 8, y: -6 }), slim({ x: 10, y: -6 }),
      slim({ x: 9, y: -7 }), slim({ x: 9, y: -5 }),
    ]);
    (r as unknown as { paint: () => void }).paint();

    const pipe = roundRect.mock.calls.find(([, , w, h]) => w !== h);
    expect(pipe).toBeDefined();
    const [, , width, height] = pipe!;
    expect(height).toBeGreaterThan(width);
    expect(height / width).toBeCloseTo(7.5);
  });
});

describe("MapRenderer listener lifecycle", () => {
  let rafSpy: ReturnType<typeof vi.spyOn>;
  let rafQueue: FrameRequestCallback[];

  beforeEach(() => {
    // Deferred rAF: queue callbacks (like a real browser) so the render()
    // coalescing guard is observable. flushRaf() runs the queued paints.
    rafQueue = [];
    rafSpy = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((cb: FrameRequestCallback) => {
        rafQueue.push(cb);
        return rafQueue.length;
      });
  });

  afterEach(() => {
    rafSpy.mockRestore();
    vi.restoreAllMocks();
  });

  function flushRaf() {
    const q = rafQueue;
    rafQueue = [];
    for (const cb of q) cb(0);
  }

  it("constructs with a 2d context and rAF-coalesces render()", () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    rafSpy.mockClear();
    // Two render() calls before the frame runs → the guard means only ONE rAF
    // is scheduled (they coalesce into a single paint).
    r.render();
    r.render();
    expect(rafSpy).toHaveBeenCalledTimes(1);
    // After the frame runs, a fresh render() schedules again.
    flushRaf();
    r.render();
    expect(rafSpy).toHaveBeenCalledTimes(2);
  });

  it("destroy() tears down window listeners so later global mouse moves do not schedule paints", () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    // Prime a drag so the window mousemove handler would otherwise call render().
    cv.dispatchEvent(new MouseEvent("mousedown", { clientX: 10, clientY: 10 }));

    r.destroy();
    rafSpy.mockClear();

    // A global mousemove after destroy() must not reach the (aborted) handler.
    window.dispatchEvent(
      new MouseEvent("mousemove", { clientX: 50, clientY: 50 }),
    );
    expect(rafSpy).not.toHaveBeenCalled();
  });

  it("render() is a no-op after destroy()", () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    r.destroy();
    rafSpy.mockClear();
    r.render();
    expect(rafSpy).not.toHaveBeenCalled();
  });
});

describe("MapRenderer seam clicks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sends a seam room through the ordinary room click callback", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    const room: SlimRoom = {
      z: "Город", t: 1, h: "Восточные ворота",
      x: 0, y: 0, l: 0, e: [], d: [], ch: 0,
      se: [
        { o: 0, k: "seam", r: "parsed", p: true, tz: "Восточная дорога", tt: 41, c: "на восток" },
        { o: 1, k: "seam", r: "parsed", p: true, tz: "Северная дорога", tt: 42, c: "на север" },
      ],
      p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0,
      m: [], mb: [], g: [], gf: [],
    };
    renderer.setZone("Город", [room]);
    renderer.centerOn(room.x, room.y, room.l);

    const seen: string[][] = [];
    renderer.onRoomClick = (clicked) => seen.push(clicked.se.map((edge) => edge.tz));

    cv.dispatchEvent(new MouseEvent("click", { clientX: 300, clientY: 200 }));

    expect(seen).toEqual([
      ["Восточная дорога", "Северная дорога"],
    ]);
  });
});

describe("MapRenderer cross-zone boundaries", () => {
  afterEach(() => vi.restoreAllMocks());

  it("rings seam and cross-zone grid rooms, with a directional grid boundary notch", () => {
    const cv = makeCanvas();
    const ctx = cv.getContext("2d") as unknown as { strokeStyle: string; stroke: () => void; moveTo: () => void };
    const strokes: string[] = [];
    const moves: unknown[][] = [];
    const stroke = ctx.stroke;
    const moveTo = ctx.moveTo;
    Object.defineProperties(ctx, {
      stroke: { configurable: true, value: () => { strokes.push(ctx.strokeStyle); stroke(); } },
      moveTo: { configurable: true, value: (...args: unknown[]) => { moves.push(args); moveTo(); } },
    });
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [
      slim({ x: 0, se: [{ o: 0, k: "seam", r: "manual", p: true, tz: "Other", c: "go" }] }),
      slim({ x: 1, xz: [{ dir: "E", r: "manual", p: false, z: "Other", x: 3, y: 4, l: 0 }, { dir: "S", r: "parsed", p: true, z: "Other", x: 5, y: 4, l: 0 }] }),
      slim({ x: 2 }),
      slim({ x: 3, p: true, xz: [{ dir: "W", r: "manual", p: true, z: "Other", x: 7, y: 8, l: 0 }] }),
    ]);
    (renderer as unknown as { paint: () => void }).paint();
    expect(strokes.filter((color) => color === "#f0b64e")).toHaveLength(6);
    expect(strokes.filter((color) => color === "#ff5d5d")).toHaveLength(1);
    expect(moves.length).toBeGreaterThan(0);
  });
});

describe("MapRenderer default focus", () => {
  it("clamps large and tiny defaults while refresh and centering preserve scale", () => {
    const cv = makeCanvas();
    Object.defineProperty(cv, "clientWidth", { value: 180, configurable: true });
    Object.defineProperty(cv, "clientHeight", { value: 120, configurable: true });
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", Array.from({ length: 40 }, (_, x) => slim({ x })));
    const view = (renderer as unknown as { view: { s: number } }).view;
    expect(view.s).toBe(0.8);
    (renderer as unknown as { fit: () => void }).fit();
    expect(view.s).toBe(0.25);

    renderer.setZone("tiny", [slim()]);
    expect(view.s).toBeCloseTo(4 / 3);
    view.s = 2.5;
    renderer.replaceRooms([slim(), slim({ x: 1 })]);
    expect(view.s).toBe(2.5);
    renderer.centerOn(1, 0, 0);
    expect(view.s).toBe(2.5);
  });

  it("keeps wheel zoom within the supported interaction range", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [slim()]);
    const view = (renderer as unknown as { view: { s: number } }).view;
    for (let i = 0; i < 100; i++) cv.dispatchEvent(new WheelEvent("wheel", { deltaY: 1 }));
    expect(view.s).toBe(0.2);
    for (let i = 0; i < 100; i++) cv.dispatchEvent(new WheelEvent("wheel", { deltaY: -1 }));
    expect(view.s).toBe(6);
  });
});

// The candidate ("ghost") cell is clickable even though it is
// NOT a real room, and roomAt/pick keep their real-rooms-only contract.
describe("MapRenderer candidate (ghost) cell", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function mountWithGhost() {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    // Empty zone so nothing but the ghost can be hit; centerOn puts the coord at
    // the canvas centre, giving a deterministic click point.
    r.setZone("Z", []);
    r.setGhost({ x: 1, y: 1, l: 0, color: "#f0b64e", label: "Новая" });
    r.centerOn(1, 1, 0);
    return { cv, r, cx: 300, cy: 200 };
  }

  it("reports a click on the candidate cell through onGhostClick", () => {
    const { cv, r, cx, cy } = mountWithGhost();
    const seen: Array<{ x: number; y: number }> = [];
    r.onGhostClick = (g) => seen.push({ x: g.x, y: g.y });
    cv.dispatchEvent(new MouseEvent("click", { clientX: cx, clientY: cy }));
    expect(seen).toEqual([{ x: 1, y: 1 }]);
  });

  it("does not fire for a click away from the candidate cell", () => {
    const { cv, r } = mountWithGhost();
    const seen: unknown[] = [];
    r.onGhostClick = (g) => seen.push(g);
    cv.dispatchEvent(new MouseEvent("click", { clientX: 10, clientY: 10 }));
    expect(seen).toEqual([]);
  });

  it("ignores the candidate on another floor", () => {
    const { cv, r, cx, cy } = mountWithGhost();
    r.setGhost({ x: 1, y: 1, l: 4, color: "#f0b64e" });
    const seen: unknown[] = [];
    r.onGhostClick = (g) => seen.push(g);
    cv.dispatchEvent(new MouseEvent("click", { clientX: cx, clientY: cy }));
    expect(seen).toEqual([]);
  });

  it("never leaks the candidate into pick() — it is not a real room", () => {
    const { r, cx, cy } = mountWithGhost();
    expect(r.pick(cx, cy)).toBeNull();
  });

  it("still paints the player marker on an EMPTY zone (the room bail must not skip it)", () => {
    const cv = makeCanvas();
    const strokes: string[] = [];
    // Record which colours get stroked so we can tell the marker ring apart from
    // the candidate outline; the marker uses its own confidence colour.
    const ctx = cv.getContext("2d") as unknown as {
      strokeStyle: string;
      stroke: () => void;
    };
    const realStroke = ctx.stroke;
    Object.defineProperty(ctx, "stroke", {
      value: () => {
        strokes.push(ctx.strokeStyle);
        realStroke?.();
      },
      configurable: true,
    });

    const r = new MapRenderer(cv);
    r.setZone("Z", []); // empty zone: the room-drawing block bails out early
    r.setGhost({ x: 1, y: 1, l: 0, color: "#f0b64e" });
    r.setMarker({ x: 1, y: 1, l: 0, color: "#54e888" });
    r.centerOn(1, 1, 0);
    (r as unknown as { paint: () => void })["paint"]();

    expect(strokes).toContain("#54e888"); // marker ring
    expect(strokes).toContain("#f0b64e"); // candidate outline
  });

  it("setGhost(null) clears it, and a redundant setGhost does not repaint", () => {
    const { cv, r, cx, cy } = mountWithGhost();
    const ghost = r.currentGhost()!;
    // A position broadcast arrives on every room; an unchanged candidate must
    // not cost a frame.
    const raf = vi.spyOn(window, "requestAnimationFrame");
    r.setGhost({ ...ghost }); // structurally identical → no new frame
    expect(raf).not.toHaveBeenCalled();

    r.setGhost(null);
    const seen: unknown[] = [];
    r.onGhostClick = (g) => seen.push(g);
    cv.dispatchEvent(new MouseEvent("click", { clientX: cx, clientY: cy }));
    expect(seen).toEqual([]);
  });
});

describe("MapRenderer scanned frontier", () => {
  it("extends beyond the player ring and draws only the near half of an unknown room", () => {
    const cv = makeCanvas();
    const ctx = cv.getContext("2d")!;
    const moveTo = vi.spyOn(ctx, "moveTo");
    const lineTo = vi.spyOn(ctx, "lineTo");
    const fillText = vi.spyOn(ctx, "fillText");
    const r = new MapRenderer(cv);
    r.setZone("Z", [slim({ h: "Перед развалинами", e: ["E"] })]);
    r.setMarker({ x: 0, y: 0, l: 0, color: "#54e888" });
    r.setFrontier({ zone: "Z", x: 0, y: 0, l: 0, hints: [{ dir: "E", hint: "Еле заметная тропинка." }] });
    r.centerOn(0, 0, 0);
    (r as unknown as { view: { s: number; ox: number; oy: number } }).view = { s: 1, ox: 300, oy: 200 };
    (r as unknown as { paint(): void }).paint();

    // CELL=30 and ROOM half=7.5 at scale 1: the corridor reaches the near face
    // at x=322.5, beyond the marker ring at x=313.5. The half-room ends at its
    // centre x=330; a full-cell outline would draw the unknown far face x=337.5.
    expect(lineTo.mock.calls.some(([x]) => x === 322.5)).toBe(true);
    expect([...moveTo.mock.calls, ...lineTo.mock.calls].some(([x]) => Number(x) > 330)).toBe(false);
    expect(fillText.mock.calls.some(([text]) => String(text).includes("Еле заметная"))).toBe(true);
    expect(r.pick(330, 200)).toBeNull();
  });

  it("highlights a scanned down exit in frontier green", () => {
    const cv = makeCanvas();
    const ctx = cv.getContext("2d")!;
    let fillStyle = "";
    const labelColors: string[] = [];
    Object.defineProperty(ctx, "fillStyle", {
      configurable: true,
      get: () => fillStyle,
      set: (value) => { fillStyle = String(value); },
    });
    vi.spyOn(ctx, "fillText").mockImplementation((text) => {
      if (String(text).includes("Черная дыра")) labelColors.push(fillStyle);
    });
    const r = new MapRenderer(cv);
    r.setZone("Хилло", [slim({ h: "В окружении ящиков" })]);
    r.setMarker({ x: 0, y: 0, l: 0, color: "#54e888" });
    r.setFrontier({ zone: "Хилло", x: 0, y: 0, l: 0, hints: [{ dir: "D", hint: "Черная дыра в полу ведет в темноту." }] });
    r.centerOn(0, 0, 0);
    (r as unknown as { paint(): void }).paint();

    expect(labelColors).toEqual(["rgba(84,232,136,.9)"]);
  });
});

// The EDGE EDITOR's click target: the boundary between two grid-adjacent rooms.
// Two hard requirements live here — it must exist even where nothing is drawn
// (marking a passage that does not exist yet is the point), and it must never
// take a click that belongs to a cell (the cell context menu already owns those).
describe("MapRenderer edge (boundary) hit zone", () => {
  it("hit-tests the boundary at presentation positions and reports the logical source", () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    const a=slim({x:0,y:0,px:10,py:10}), b=slim({x:1,y:0,px:12,py:10});
    r.setZone("Z",[a,b]);
    r.centerOn(a.x,a.y,a.l);
    const view=(r as unknown as {view:{s:number;ox:number;oy:number}}).view;
    const hits:string[]=[];
    r.onEdgeClick=(room,dir)=>hits.push(`${room.x},${room.y}:${dir}`);
    const mx=view.ox+10*30*view.s, my=view.oy+11*30*view.s;
    cv.dispatchEvent(new MouseEvent("click",{clientX:mx,clientY:my}));
    expect(hits).toEqual(["0,0:S"]);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function slim(over: Partial<SlimRoom>): SlimRoom {
    return {
      z: "Z", t: null, h: "", x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [],
      p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0,
      m: [], mb: [], g: [], gf: [], ...over,
    };
  }

  // Two rooms side by side on the screen's vertical axis: S steps x+1, and
  // screenY follows x, so (0,0) sits above (1,0).
  function mountPair() {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    const a = slim({ x: 0, y: 0, h: "A" });
    const b = slim({ x: 1, y: 0, h: "B" });
    r.setZone("Z", [a, b]);
    r.setLevel(0);
    return { cv, r, a, b };
  }

  // Screen position of a room under the renderer's current view.
  function pos(r: MapRenderer, room: SlimRoom): { x: number; y: number } {
    const v = (r as unknown as { view: { s: number; ox: number; oy: number } }).view;
    return { x: room.y * 30 * v.s + v.ox, y: room.x * 30 * v.s + v.oy };
  }

  it("reports the boundary between two adjacent rooms, canonically oriented", () => {
    const { cv, r, a, b } = mountPair();
    const pa = pos(r, a);
    const pb = pos(r, b);
    const seen: Array<{ x: number; dir: string }> = [];
    r.onEdgeClick = (room, dir) => seen.push({ x: room.x, dir });
    cv.dispatchEvent(
      new MouseEvent("click", {
        clientX: (pa.x + pb.x) / 2,
        clientY: (pa.y + pb.y) / 2,
      }),
    );
    // The boundary is owned by the +x cell's S face regardless of which half of
    // the gutter was clicked, so the popup's two blocks keep a stable order.
    expect(seen).toEqual([{ x: 0, dir: "S" }]);
  });

  it("does NOT fire for a click on a room square — the cell menu keeps its pixels", () => {
    const { cv, r, a } = mountPair();
    const pa = pos(r, a);
    const edges: unknown[] = [];
    const rooms: unknown[] = [];
    r.onEdgeClick = (room, dir) => edges.push(dir);
    r.onRoomClick = (room) => rooms.push(room.x);
    cv.dispatchEvent(new MouseEvent("click", { clientX: pa.x, clientY: pa.y }));
    expect(edges).toEqual([]);
    expect(rooms).toEqual([0]);
  });

  it("still hits the boundary at a small zoom, where the gutter is a couple of pixels", () => {
    const { cv, r, a, b } = mountPair();
    (r as unknown as { view: { s: number } }).view.s = 0.25;
    const pa = pos(r, a);
    const pb = pos(r, b);
    const seen: string[] = [];
    r.onEdgeClick = (_room, dir) => seen.push(dir);
    cv.dispatchEvent(
      new MouseEvent("click", {
        clientX: (pa.x + pb.x) / 2,
        clientY: (pa.y + pb.y) / 2,
      }),
    );
    expect(seen).toEqual(["S"]);
  });

  it("offers no boundary where the neighbouring cell holds no room", () => {
    // A hand-drawn edge needs a room on both sides; there is nothing to edit at
    // the edge of the map, and offering it would only produce a refusal.
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    const a = slim({ x: 0, y: 0, h: "A" });
    r.setZone("Z", [a]);
    r.setLevel(0);
    const pa = pos(r, a);
    const seen: unknown[] = [];
    r.onEdgeClick = () => seen.push(1);
    cv.dispatchEvent(
      new MouseEvent("click", { clientX: pa.x, clientY: pa.y + 15 }),
    );
    expect(seen).toEqual([]);
  });
});

// Review round 2: overlapping bands must resolve to the boundary under the
// cursor, not to whichever room the scan happened to reach first.
describe("MapRenderer edge hit zone picks the NEAREST boundary", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function slim(over: Partial<SlimRoom>): SlimRoom {
    return {
      z: "Z", t: null, h: "", x: 0, y: 0, l: 0, e: [], d: [], ch: 0, se: [],
      p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0,
      m: [], mb: [], g: [], gf: [], ...over,
    };
  }

  function pos(r: MapRenderer, room: { x: number; y: number }) {
    const v = (r as unknown as { view: { s: number; ox: number; oy: number } }).view;
    return { x: room.y * 30 * v.s + v.ox, y: room.x * 30 * v.s + v.oy };
  }

  // A 2x2 block, so every interior click has more than one boundary band over it
  // once the bands are wider than the gap between boundaries.
  function mountBlock(scale: number) {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    r.setZone("Z", [
      slim({ x: 1, y: 1, h: "11" }),
      slim({ x: 1, y: 2, h: "12" }),
      slim({ x: 2, y: 1, h: "21" }),
      slim({ x: 2, y: 2, h: "22" }),
    ]);
    r.setLevel(0);
    (r as unknown as { view: { s: number } }).view.s = scale;
    return { cv, r };
  }

  it("opens the boundary the click is closest to, at a zoom where bands overlap", () => {
    const { cv, r } = mountBlock(0.3);
    const a = pos(r, { x: 1, y: 1 });
    const b = pos(r, { x: 2, y: 1 });
    const seen: string[] = [];
    r.onEdgeClick = (room, dir) => seen.push(`${room.x}${room.y}:${dir}`);
    // Dead centre of the (1,1)-(2,1) boundary: the (1,1)-(1,2) band also covers
    // this point at 0.3, and used to win purely by scan order.
    cv.dispatchEvent(
      new MouseEvent("click", {
        clientX: (a.x + b.x) / 2,
        clientY: (a.y + b.y) / 2,
      }),
    );
    expect(seen).toEqual(["11:S"]);
  });

  it("resolves the perpendicular neighbour when the click moves to ITS midpoint", () => {
    const { cv, r } = mountBlock(0.3);
    const a = pos(r, { x: 1, y: 1 });
    const b = pos(r, { x: 1, y: 2 });
    const seen: string[] = [];
    r.onEdgeClick = (room, dir) => seen.push(`${room.x}${room.y}:${dir}`);
    cv.dispatchEvent(
      new MouseEvent("click", {
        clientX: (a.x + b.x) / 2,
        clientY: (a.y + b.y) / 2,
      }),
    );
    expect(seen).toEqual(["11:E"]);
  });

  it("keeps picking the nearest boundary at the wheel's minimum zoom", () => {
    const { cv, r } = mountBlock(0.2);
    const a = pos(r, { x: 1, y: 1 });
    const b = pos(r, { x: 2, y: 1 });
    const seen: string[] = [];
    r.onEdgeClick = (room, dir) => seen.push(`${room.x}${room.y}:${dir}`);
    cv.dispatchEvent(
      new MouseEvent("click", {
        clientX: (a.x + b.x) / 2,
        clientY: (a.y + b.y) / 2,
      }),
    );
    expect(seen).toEqual(["11:S"]);
  });
});

describe("MapRenderer selection mode", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("toggles a room without opening its ordinary cell callback", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    const room = slim({ x: 0, y: 0, px: 20, py: 30, pl: 2 });
    renderer.setZone("Z", [room]);
    renderer.setSelectionMode(true);
    const selected: unknown[] = [];
    const opened: unknown[] = [];
    renderer.onSelectionChange = (coords) => selected.push(coords);
    renderer.onRoomClick = () => opened.push(1);
    cv.dispatchEvent(new MouseEvent("mousedown", { clientX: 300, clientY: 200 }));
    window.dispatchEvent(new MouseEvent("mouseup", { clientX: 300, clientY: 200 }));
    cv.dispatchEvent(new MouseEvent("click", { clientX: 300, clientY: 200 }));
    expect(selected).toEqual([[{ x: 0, y: 0, l: 0 }]]);
    expect(opened).toEqual([]);
  });

  it("uses a drag rectangle for visible-level rooms and does not pan", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [slim({ x: 0, y: 0 }), slim({ x: 0, y: 1 }), slim({ x: 0, y: 2, l: 1 })]);
    renderer.setSelectionMode(true);
    const seen: Array<Array<{ x: number; y: number; l: number }>> = [];
    renderer.onSelectionChange = (coords) => seen.push(coords);
    const before = { ...(renderer as unknown as { view: { ox: number; oy: number } }).view };
    cv.dispatchEvent(new MouseEvent("mousedown", { clientX: before.ox - 10, clientY: before.oy - 10 }));
    window.dispatchEvent(new MouseEvent("mousemove", { clientX: before.ox + 200, clientY: before.oy + 10 }));
    window.dispatchEvent(new MouseEvent("mouseup", { clientX: before.ox + 200, clientY: before.oy + 10 }));
    expect(seen.at(-1)).toEqual([{ x: 0, y: 0, l: 0 }, { x: 0, y: 1, l: 0 }]);
    expect((renderer as unknown as { view: { ox: number; oy: number } }).view).toEqual(before);
  });

  it("rectangle-selects by presentation position/floor but emits logical coordinates", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [
      slim({ x: 10, y: 10, l: 0, px: 0, py: 0, pl: 2 }),
      slim({ x: 11, y: 11, l: 0, px: 0, py: 1, pl: 3 }),
    ]);
    renderer.setLevel(2);
    const view = (renderer as unknown as { view: { ox:number; oy:number } }).view;
    (renderer as unknown as { selectRect(a:{x:number;y:number}, b:{x:number;y:number}, toggle:boolean):void })
      .selectRect({x:view.ox-10,y:view.oy-10},{x:view.ox+10,y:view.oy+10},false);
    expect(renderer.selectedCoords()).toEqual([{ x: 10, y: 10, l: 0 }]);
  });

  it("keeps panning and boundary editing outside selection mode", () => {
    const { cv, r, a, b } = (() => {
      const cv = makeCanvas();
      const r = new MapRenderer(cv);
      const a = slim({ x: 0, y: 0 }), b = slim({ x: 1, y: 0 });
      r.setZone("Z", [a, b]);
      return { cv, r, a, b };
    })();
    const view = (r as unknown as { view: { s: number; ox: number; oy: number } }).view;
    cv.dispatchEvent(new MouseEvent("mousedown", { clientX: 20, clientY: 20 }));
    window.dispatchEvent(new MouseEvent("mousemove", { clientX: 50, clientY: 20 }));
    window.dispatchEvent(new MouseEvent("mouseup", { clientX: 50, clientY: 20 }));
    expect(view.ox).not.toBe(0);
    const edges: string[] = [];
    r.onEdgeClick = (_room, dir) => edges.push(dir);
    // Restore a deterministic view for the midpoint hit.
    view.s = 1; view.ox = 300; view.oy = 200;
    cv.dispatchEvent(new MouseEvent("mousedown", { clientX: 10, clientY: 10 }));
    window.dispatchEvent(new MouseEvent("mouseup", { clientX: 10, clientY: 10 }));
    cv.dispatchEvent(new MouseEvent("click", { clientX: 300, clientY: 215 }));
    expect(edges).toEqual(["S"]);
    void a; void b;
  });

  it("uses Shift rectangle as a deterministic add/subtract toggle", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [slim({ x: 0, y: 0 }), slim({ x: 0, y: 1 })]);
    renderer.setSelectionMode(true);
    const view = (renderer as unknown as { view: { ox: number; oy: number } }).view;
    const selected = (renderer as unknown as { selected: Set<string> }).selected;
    selected.add("0,0,0");
    selected.add("0,1,0");
    expect(renderer.selectedCoords()).toHaveLength(2);

    cv.dispatchEvent(new MouseEvent("mousedown", { clientX: view.ox - 10, clientY: view.oy - 10 }));
    window.dispatchEvent(new MouseEvent("mousemove", { clientX: view.ox + 400, clientY: view.oy + 10 }));
    window.dispatchEvent(new MouseEvent("mouseup", {
      clientX: view.ox + 400,
      clientY: view.oy + 10,
      shiftKey: true,
    }));
    expect(renderer.selectedCoords()).toEqual([]);
  });

  it("selects with a touch pointer rectangle", () => {
    vi.stubGlobal("PointerEvent", MouseEvent);
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [slim({ x: 0, y: 0 }), slim({ x: 0, y: 1 })]);
    renderer.setSelectionMode(true);
    const view = (renderer as unknown as { view: { ox: number; oy: number } }).view;
    const pointer = (type: string, x: number, y: number) => {
      const event = new MouseEvent(type, { clientX: x, clientY: y, bubbles: true });
      Object.defineProperties(event, {
        pointerId: { value: 4 },
        pointerType: { value: "touch" },
        isPrimary: { value: true },
      });
      return event;
    };
    cv.dispatchEvent(pointer("pointerdown", view.ox - 10, view.oy - 10));
    cv.dispatchEvent(pointer("pointermove", view.ox + 400, view.oy + 10));
    cv.dispatchEvent(pointer("pointerup", view.ox + 400, view.oy + 10));
    expect(renderer.selectedCoords()).toEqual([
      { x: 0, y: 0, l: 0 },
      { x: 0, y: 1, l: 0 },
    ]);
  });

  it("caps a rectangle selection at the REST operation limit", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", Array.from({ length: 8001 }, (_, y) => slim({ y })));
    (renderer as unknown as {
      selectRect(start: { x: number; y: number }, end: { x: number; y: number }, toggle: boolean): void;
    }).selectRect({ x: -1e9, y: -1e9 }, { x: 1e9, y: 1e9 }, false);
    expect(renderer.selectedCoords()).toHaveLength(8000);
  });

  it("uses two touch pointers to zoom without committing a selection", () => {
    vi.stubGlobal("PointerEvent", MouseEvent);
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [slim()]);
    renderer.setSelectionMode(true);
    const pointer = (type: string, id: number, x: number, y: number) => {
      const event = new MouseEvent(type, { clientX: x, clientY: y, bubbles: true });
      Object.defineProperties(event, {
        pointerId: { value: id }, pointerType: { value: "touch" }, isPrimary: { value: id === 1 },
      });
      return event;
    };
    const view = (renderer as unknown as { view: { s: number } }).view;
    view.s = 1;
    const before = view.s;
    cv.dispatchEvent(pointer("pointerdown", 1, 100, 100));
    cv.dispatchEvent(pointer("pointerdown", 2, 200, 100));
    cv.dispatchEvent(pointer("pointermove", 2, 300, 100));
    cv.dispatchEvent(pointer("pointerup", 2, 300, 100));
    cv.dispatchEvent(pointer("pointerup", 1, 100, 100));
    expect(view.s).toBeGreaterThan(before);
    expect(renderer.selectedCoords()).toEqual([]);
  });

  it("keeps a tiny zone within the supported zoom range", () => {
    const cv = makeCanvas();
    const renderer = new MapRenderer(cv);
    renderer.setZone("Z", [slim()]);
    expect((renderer as unknown as { view: { s: number } }).view.s).toBeLessThanOrEqual(1.8);
  });

  it("releases an active pointer capture when the renderer is destroyed", () => {
    vi.stubGlobal("PointerEvent", MouseEvent);
    const cv = makeCanvas();
    const captured = new Set<number>();
    const release = vi.fn((id: number) => { captured.delete(id); });
    Object.defineProperties(cv, {
      setPointerCapture: { value: vi.fn((id: number) => { captured.add(id); }), configurable: true },
      hasPointerCapture: { value: vi.fn((id: number) => captured.has(id)), configurable: true },
      releasePointerCapture: { value: release, configurable: true },
    });
    const renderer = new MapRenderer(cv);
    const down = new MouseEvent("pointerdown", { clientX: 10, clientY: 10, button: 0 });
    Object.defineProperties(down, {
      pointerId: { value: 8 }, pointerType: { value: "touch" }, isPrimary: { value: true },
    });
    cv.dispatchEvent(down);
    renderer.destroy();
    expect(release).toHaveBeenCalledWith(8);
    expect(captured.size).toBe(0);
  });
});
