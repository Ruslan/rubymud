import {
  computeBounds,
  defaultLevel,
  displayLevel,
  DIR_SCREEN_VEC,
  levelsOf,
  screenX,
  screenY,
  stepDelta,
  type Bounds,
} from "./geometry";
import type { SlimRoom } from "./types";

// The renderer is instance-based (no global DOM). One MapRenderer owns one
// <canvas>, its view transform, the active zone's rooms, the active floor, and
// an optional "you are here" marker. Read-only. Room thumbnails/images are
// currently out of scope; draw() leaves the extension point in place.

const CELL = 30;
const ROOM = 15;
const MAX_SELECTION = 8000;

type PipeAxis = "h" | "v" | null;

interface PipeNeighbours {
  hL: boolean;
  hR: boolean;
  vU: boolean;
  vD: boolean;
}

// `ch` is the map's authoritative connectivity. Geometry is only a fallback:
// a pipe may share a grid row/column with unrelated rooms, as in a crossing.
export function pipeAxis(ch: number, neighbours: PipeNeighbours): PipeAxis {
  const northSouth = ch & 0b0011;
  const eastWest = ch & 0b1100;
  if (northSouth !== 0 && eastWest === 0) return "v";
  if (eastWest !== 0 && northSouth === 0) return "h";

  const { hL, hR, vU, vD } = neighbours;
  const horz = hL || hR;
  const vert = vU || vD;
  if (horz && vert) return hL && hR ? "h" : vU && vD ? "v" : "h";
  if (horz) return "h";
  if (vert) return "v";
  return null;
}

// imageindex to emoji glyph mapping used by map room markers.
const EMOJI: Record<number, string> = {
  1: "🌑",
  3: "👹",
  4: "🛠️",
  5: "⚔️",
  6: "🔮",
  8: "🔨",
  9: "📦",
  11: "🥷",
  12: "⛪",
  14: "🛡️",
  15: "💎",
  17: "🍺",
  18: "⛲",
  19: "🏦",
  20: "🌲",
};

const COL = {
  line: "#1b2a20",
  phos: "#54e888",
  amber: "#f0b64e",
  red: "#ff5d5d",
  ink: "#cfe6d6",
  ground: "#090c0a",
};

interface View {
  s: number;
  ox: number;
  oy: number;
}

// The current player marker in game coords (drawn only when on the active
// zone+floor). Confidence tints the ring color.
export interface Marker {
  x: number;
  y: number;
  l: number;
  color: string; // ring color
}

// A CANDIDATE cell: the coord the player occupies that has no room in the map
// yet (tracker 🟡 "edge/cell not in map"). Drawn as a dashed outline in the
// confidence colour so the operator can see what «Записать комнату сюда» would
// create, with the live room name beside it when one is known.
export interface GhostCell {
  x: number;
  y: number;
  l: number;
  color: string;
  label?: string;
}

export interface FrontierHint {
  dir: string;
  hint: string;
}

// Ephemeral scan result for the confirmed current room. It is deliberately not
// a SlimRoom: it has no identity, click target, route node, or persistence.
export interface ScannedFrontier {
  zone: string;
  x: number;
  y: number;
  l: number;
  hints: FrontierHint[];
}

export type RoomClickHandler = (room: SlimRoom) => void;

export type ZoneCoord = { x: number; y: number; l: number };

export type SelectionChange = (coords: ZoneCoord[]) => void;

export class MapRenderer {
  private ctx: CanvasRenderingContext2D;
  private rooms: SlimRoom[] = [];
  private coords = new Set<string>();
  private bounds: Bounds = { minx: 0, miny: 0, maxx: 0, maxy: 0 };
  private levels: number[] = [];
  level = 0;
  zoneName = "";
  private view: View = { s: 1, ox: 0, oy: 0 };
  private marker: Marker | null = null;
  private ghost: GhostCell | null = null;
  private frontier: ScannedFrontier | null = null;
  // Selection is intentionally canvas-owned. Even an 8k-room operation keeps
  // one Set and one O(visible rooms) paint pass, never a DOM node per room.
  private selectionMode = false;
  private selected = new Set<string>();
  private selecting = false;
  private selectStart: { x: number; y: number } | null = null;
  private selectEnd: { x: number; y: number } | null = null;
  private dragging = false;
  private lastX = 0;
  private lastY = 0;
  private moved = false;
  private activePointerID: number | null = null;
  private activePointerButton = 0;
  private touchPointers = new Map<number, { x: number; y: number }>();
  private pinch: { distance: number; x: number; y: number; view: View } | null = null;
  private gestureHadPinch = false;
  // One AbortController owns every event listener (canvas + window) so destroy()
  // can tear all of them down in one call — no leaked zombie window handlers.
  private listeners = new AbortController();
  // rAF coalescing: repeated render() calls within a frame collapse to one paint.
  private renderScheduled = false;
  private destroyed = false;
  // Observes the canvas element so a post-mount size change (status line appears,
  // split drag, window resize) re-syncs the backing store to the CSS size — the
  // fix for the "murk" band of stale pixels at the bottom of the map. Torn down
  // in destroy().
  private resizeObserver: ResizeObserver | null = null;

  // Callbacks the pane controller wires up.
  onRoomClick?: (room: SlimRoom, clientX: number, clientY: number) => void;
  // Click on the candidate (ghost) cell — there is no SlimRoom to pass because
  // the cell does not exist in the map yet.
  onGhostClick?: (ghost: GhostCell, clientX: number, clientY: number) => void;
  // Click on the BOUNDARY between `room` and its grid neighbour in `dir` — the
  // edge editor. `room`/`dir` identify the boundary canonically (see edgeAt).
  onEdgeClick?: (
    room: SlimRoom,
    dir: string,
    clientX: number,
    clientY: number,
  ) => void;
  onRoomHover?: (
    room: SlimRoom | null,
    clientX: number,
    clientY: number,
  ) => void;
  onSelectionChange?: SelectionChange;
  onLevelChange?: (level: number) => void;

  constructor(public readonly canvas: HTMLCanvasElement) {
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("2d canvas context unavailable");
    this.ctx = ctx;
    this.attach();
  }

  // Remove every event listener and stop future paints. Idempotent. Called by
  // MapPaneController.dispose() on split/close/rebuild/buffer-switch.
  destroy(): void {
    this.destroyed = true;
    this.resizeObserver?.disconnect();
    this.resizeObserver = null;
    this.listeners.abort();
    const captured = new Set(this.touchPointers.keys());
    if (this.activePointerID != null) captured.add(this.activePointerID);
    for (const pointerID of captured) {
      if (this.canvas.hasPointerCapture?.(pointerID)) this.canvas.releasePointerCapture?.(pointerID);
    }
    this.activePointerID = null;
    this.touchPointers.clear();
    this.pinch = null;
  }

  // Load a zone's rooms and reset the view to a readable default focus.
  setZone(zoneName: string, rooms: SlimRoom[]): void {
    this.zoneName = zoneName;
    this.rooms = rooms;
    this.coords = new Set(rooms.map((r) => this.key(r)));
    this.bounds = computeBounds(rooms);
    this.levels = levelsOf(rooms);
    this.level = defaultLevel(rooms);
    this.clearSelection(false);
    this.focusDefault();
    this.render();
  }

  // Replace the current zone's corpus without disturbing an operator's camera.
  // A refresh can remove the active floor, in which case choose the normal
  // default rather than leaving the renderer on an empty layer.
  replaceRooms(rooms: SlimRoom[]): void {
    const previousLevel = this.level;
    this.rooms = rooms;
    this.coords = new Set(rooms.map((r) => this.key(r)));
    this.bounds = computeBounds(rooms);
    this.levels = levelsOf(rooms);
    this.level = this.levels.includes(previousLevel)
      ? previousLevel
      : defaultLevel(rooms);
    // A refresh can only retain coords still present in the displayed zone.
    for (const key of this.selected) if (!this.coords.has(key)) this.selected.delete(key);
    this.emitSelection();
    this.render();
  }

  clear(): void {
    this.zoneName = "";
    this.rooms = [];
    this.coords.clear();
    this.bounds = { minx: 0, miny: 0, maxx: 0, maxy: 0 };
    this.levels = [];
    this.level = 0;
    this.marker = null;
    this.ghost = null;
    this.frontier = null;
    this.clearSelection(false);
    this.view = { s: 1, ox: 0, oy: 0 };
    this.render();
  }

  levelList(): number[] {
    return this.levels;
  }

  setLevel(l: number): void {
    if (l === this.level) return;
    this.level = l;
    this.onLevelChange?.(l);
    this.render();
  }

  setSelectionMode(active: boolean): void {
    if (this.selectionMode === active) return;
    this.selectionMode = active;
    this.selecting = false;
    this.selectStart = null;
    this.selectEnd = null;
    this.canvas.classList.toggle("select", active);
    this.render();
  }

  isSelectionMode(): boolean { return this.selectionMode; }

  selectedCoords(): ZoneCoord[] {
    const out: ZoneCoord[] = [];
    for (const room of this.rooms) {
      if (this.selected.has(this.key(room))) out.push({ x: room.x, y: room.y, l: room.l });
    }
    return out;
  }

  clearSelection(notify = true): void {
    if (this.selected.size === 0) return;
    this.selected.clear();
    if (notify) this.emitSelection();
    this.render();
  }

  private emitSelection(): void {
    this.onSelectionChange?.(this.selectedCoords());
  }

  // Place/clear the "you are here" marker. Re-centers only via followTo().
  setMarker(m: Marker | null): void {
    this.marker = m;
    this.render();
  }

  // Place/clear the candidate (ghost) cell. Cheap no-op when nothing changed so
  // the per-position refresh does not schedule a paint on every broadcast.
  setGhost(g: GhostCell | null): void {
    if (sameGhost(this.ghost, g)) return;
    this.ghost = g;
    this.render();
  }

  currentGhost(): GhostCell | null {
    return this.ghost;
  }

  setFrontier(frontier: ScannedFrontier | null): void {
    if (sameFrontier(this.frontier, frontier)) return;
    this.frontier = frontier;
    this.render();
  }

  // Center the view on a game coord and (optionally) switch floors, used by both
  // seam jumps and follow-player. Assumes the coord belongs to the loaded zone.
  centerOn(x: number, y: number, l: number): void {
    const room = this.findRoom(x, y, l);
    const targetLevel = room ? displayLevel(room) : l;
    const targetX = room ? screenY(room) : x;
    const targetY = room ? screenX(room) : y;
    // A fresh autorecord position is published before rooms_changed refetches
    // the zone. Follow must remember that not-yet-cached level; replaceRooms
    // will then preserve it once the committed room arrives. The same behaviour
    // makes a vertical predicted ghost visible while its room is still pending.
    if (targetLevel !== this.level) {
      this.level = targetLevel;
      this.onLevelChange?.(targetLevel);
    }
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    this.view.ox = w / 2 - targetY * CELL * this.view.s;
    this.view.oy = h / 2 - targetX * CELL * this.view.s;
    this.render();
  }

  findRoom(x: number, y: number, l: number): SlimRoom | undefined {
    return this.rooms.find((r) => r.x === x && r.y === y && r.l === l);
  }

  // Resize backing store to the element's CSS size (call on mount/resize).
  // Skips a no-op resize so a burst of ResizeObserver ticks at the same size
  // doesn't thrash the backing store (which would blank it). Returns true when
  // the backing store actually changed.
  resize(): boolean {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const w = Math.max(1, Math.round(this.canvas.clientWidth * dpr));
    const h = Math.max(1, Math.round(this.canvas.clientHeight * dpr));
    if (this.canvas.width === w && this.canvas.height === h) return false;
    this.canvas.width = w;
    this.canvas.height = h;
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.render();
    return true;
  }

  private key(r: { x: number; y: number; l: number }): string {
    return `${r.x},${r.y},${r.l}`;
  }

  private sx(r: SlimRoom): number {
    return screenX(r) * CELL * this.view.s + this.view.ox;
  }
  private sy(r: SlimRoom): number {
    return screenY(r) * CELL * this.view.s + this.view.oy;
  }

  private focusDefault(): void {
    const w = this.canvas.clientWidth || 600;
    const h = this.canvas.clientHeight || 400;
    const b = this.bounds;
    const gw = (b.maxx - b.minx + 1) * CELL;
    const gh = (b.maxy - b.miny + 1) * CELL;
    const fitS = Math.min((w - 80) / gw, (h - 80) / gh);
    // Initial focus should leave a large zone readable. Explicit Fit remains
    // allowed to use the full overview range below.
    this.view.s = Math.max(0.8, Math.min(fitS, 1.8, 40 / CELL));
    let sx = 0;
    let sy = 0;
    if (this.rooms.length > 0) {
      for (const r of this.rooms) {
        sx += screenX(r);
        sy += screenY(r);
      }
      const cx = sx / this.rooms.length;
      const cy = sy / this.rooms.length;
      this.view.ox = w / 2 - cx * CELL * this.view.s;
      this.view.oy = h / 2 - cy * CELL * this.view.s;
    }
  }

  private fit(): void {
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    const b = this.bounds;
    const gw = (b.maxx - b.minx + 1) * CELL;
    const gh = (b.maxy - b.miny + 1) * CELL;
    const s = Math.min((w - 80) / gw, (h - 80) / gh, 2.2);
    this.view.s = Math.max(0.25, s);
    this.view.ox = w / 2 - ((b.minx + b.maxx) / 2) * CELL * this.view.s;
    this.view.oy = h / 2 - ((b.miny + b.maxy) / 2) * CELL * this.view.s;
  }

  // Public entry point: coalesce repeated calls within a frame into a single
  // paint. room_position updates, drag mousemove, and wheel all funnel here, so
  // a burst of events costs one clearRect + O(rooms) pass per animation frame,
  // not one per event.
  render(): void {
    if (this.destroyed || this.renderScheduled) return;
    this.renderScheduled = true;
    requestAnimationFrame(() => {
      this.renderScheduled = false;
      if (this.destroyed) return;
      this.paint();
    });
  }

  private paint(): void {
    const ctx = this.ctx;
    // Clear the ENTIRE backing store in device pixels, independent of the dpr
    // transform. A CSS-pixel clearRect(0,0,clientW,clientH) leaves a stale band
    // whenever the backing store is taller than clientH*dpr (the resize artifact:
    // the element grew/shrank between the last resize() and this paint). Resetting
    // the transform to identity for the clear guarantees full coverage; the dpr
    // transform is restored immediately after.
    const byLogical = new Map(this.rooms.map((r) => [`${r.z}\0${r.x}\0${r.y}\0${r.l}`, r]));
    ctx.save();
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    ctx.restore();
    const S = this.view.s;
    const half = (ROOM * S) / 2;
    const AL = this.level;

    // Candidate cell (no room in the map at the player's coord): a DASHED
    // outline on the empty background, so it reads as "not real yet" next to the
    // solid room squares. Drawn before the rooms and before the empty-zone bail
    // — a zone with no rooms loaded is exactly when a candidate matters most —
    // and the marker ring still paints on top at the end.
    if (this.ghost && this.ghost.l === AL) {
      this.paintGhost(this.ghost, S, half);
    }
    if (this.rooms.length === 0) {
      // Nothing to draw for rooms — but the player marker still must paint.
      this.paintScannedFrontier(S, half, AL);
      this.paintMarker(S, half, AL);
      return;
    }

    // Ghost rooms of inactive levels (faint underlay).
    ctx.fillStyle = "rgba(47,138,82,.12)";
    for (const r of this.rooms) {
      if (displayLevel(r) === AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      this.roundRect(cx - half, cy - half, half * 2, half * 2, 2 * S);
      ctx.fill();
    }

    // Selection is a background affordance: every topology mark and room glyph
    // stays legible above it.
    this.paintSelection(S, half, AL);

    // Exit stubs (active level). Drawn at the same thickness + bright fill as the
    // narrow-corridor (pipe/tunnel) bars below, so simple corridors and tunnels
    // read as one uniform connector weight.
    ctx.lineWidth = Math.max(2, 4 * S);
    ctx.strokeStyle = "rgba(47,138,82,.6)";
    for (const r of this.rooms) {
      if (displayLevel(r) !== AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      for (const d of r.e) {
        // A face the player has DENIED by hand gets no ordinary stub even though
        // the exits line still names it: the room still prints the exit, the graph
        // no longer has it, and the map must show the graph. The negation marker
        // below is drawn there instead.
        if (r.mb.includes(d)) continue;
        const v = DIR_SCREEN_VEC[d];
        if (!v) continue;
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(cx + v[0] * CELL * S * 0.5, cy + v[1] * CELL * S * 0.5);
        ctx.stroke();
      }
    }

    // The materialized graph can know about a real passage that the MUD omits
    // from its printed exits (hidden buttons, concealed paths, legacy map facts).
    // Keep provenance distinct from a manual amber mark: green still means the
    // parsed map graph, while the dash says "not announced by this room".
    ctx.save();
    ctx.strokeStyle = "rgba(47,138,82,.75)";
    ctx.setLineDash([Math.max(2, 3 * S), Math.max(2, 3 * S)]);
    for (const r of this.rooms) {
      if (displayLevel(r) !== AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      for (const d of r.g) {
        if (r.e.includes(d) || r.mb.includes(d)) continue;
        const v = DIR_SCREEN_VEC[d];
        if (!v) continue;
        const delta = stepDelta(d);
        const target = delta ? byLogical.get(`${r.z}\0${r.x + delta.dx}\0${r.y + delta.dy}\0${r.l + delta.dl}`) : undefined;
        if (target && Math.hypot(this.sx(target) - cx, this.sy(target) - cy) > CELL * S * 1.01) continue;
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(cx + v[0] * CELL * S * 0.5, cy + v[1] * CELL * S * 0.5);
        ctx.stroke();
      }
    }
    ctx.setLineDash([]);
    ctx.restore();

    this.paintHandDrawn(S, AL);

    // Exact corridors are a single topology hop even when presentation layout
    // puts their endpoints far apart. Paint one clipped primitive and never
    // synthesize intermediate rooms/cells.
    ctx.lineWidth = Math.max(2, 4 * S);
    ctx.strokeStyle = "rgba(47,138,82,.75)";
    for (const from of this.rooms) {
      if (displayLevel(from) !== AL) continue;
      for (const edge of from.co ?? []) {
        if (!edge.p || edge.z !== from.z) continue;
        const to = byLogical.get(`${edge.z}\0${edge.x}\0${edge.y}\0${edge.l}`);
        if (!to || displayLevel(to) !== AL) continue;
        const ax = this.sx(from), ay = this.sy(from), bx = this.sx(to), by = this.sy(to);
        const dx = bx - ax, dy = by - ay, distance = Math.hypot(dx, dy);
        if (distance <= half * 2) continue;
        const clip = half / distance;
        ctx.beginPath();
        ctx.moveTo(ax + dx * clip, ay + dy * clip);
        ctx.lineTo(bx - dx * clip, by - dy * clip);
        ctx.stroke();
      }
    }
    // A normal grid edge can also become visually long after relayout (the
    // five-room loop moves room 5 under room 1, stretching old 4–5). Draw one
    // primitive for the physical pair; two directed rows must not double-paint it.
    const paintedGrid = new Set<string>();
    for (const from of this.rooms) {
      if (displayLevel(from) !== AL) continue;
      for (const dir of [...from.m, ...from.g, ...from.gf]) {
        const delta = stepDelta(dir); if (!delta) continue;
        const targetKey = `${from.z}\0${from.x + delta.dx}\0${from.y + delta.dy}\0${from.l + delta.dl}`;
        const to = byLogical.get(targetKey); if (!to || displayLevel(to) !== AL) continue;
        const sourceKey = `${from.z}\0${from.x}\0${from.y}\0${from.l}`;
        const pair = sourceKey < targetKey ? `${sourceKey}|${targetKey}` : `${targetKey}|${sourceKey}`;
        if (paintedGrid.has(pair)) continue;
        const ax=this.sx(from),ay=this.sy(from),bx=this.sx(to),by=this.sy(to),dx=bx-ax,dy=by-ay,distance=Math.hypot(dx,dy);
        if (distance <= CELL * S * 1.01) continue;
        paintedGrid.add(pair);
        const clip=half/distance;ctx.beginPath();ctx.moveTo(ax+dx*clip,ay+dy*clip);ctx.lineTo(bx-dx*clip,by-dy*clip);ctx.stroke();
      }
    }

    // Rooms (active level).
    for (const r of this.rooms) {
      if (displayLevel(r) !== AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      const up = r.e.includes("U");
      const dn = r.e.includes("D");

      if (r.p) {
        // Narrow corridor cell. `ch` wins over geometry, which is ambiguous in
        // crossings; screenX=y, screenY=x.
        const co = this.coords;
        const hL = co.has(`${r.x},${r.y - 1},${r.l}`);
        const hR = co.has(`${r.x},${r.y + 1},${r.l}`);
        const vU = co.has(`${r.x - 1},${r.y},${r.l}`);
        const vD = co.has(`${r.x + 1},${r.y},${r.l}`);
        ctx.fillStyle = r.s ? "rgba(255,93,93,.7)" : "rgba(47,138,82,.6)";
        const thin = Math.max(2, 4 * S);
        const span = CELL * S;
        const axis = pipeAxis(r.ch, { hL, hR, vU, vD });
        if (axis === "h") {
          this.roundRect(cx - span / 2, cy - thin / 2, span, thin, 1 * S);
          ctx.fill();
        } else if (axis === "v") {
          this.roundRect(cx - thin / 2, cy - span / 2, thin, span, 1 * S);
          ctx.fill();
        } else {
          ctx.beginPath();
          ctx.arc(cx, cy, Math.max(1.5, 2 * S), 0, 7);
          ctx.fill();
        }
      } else {
        // Named room square. A future room-thumbnail layer would draw here in
        // place of the flat fill; the seam is intentionally left open.
        const emo = r.i != null ? EMOJI[r.i] : undefined;
        ctx.fillStyle = r.s
          ? "rgba(255,93,93,.9)"
          : emo
            ? "rgba(47,138,82,.6)"
            : "rgba(47,138,82,.85)";
        this.roundRect(cx - half, cy - half, half * 2, half * 2, 3 * S);
        ctx.fill();
        if (emo && half > 5) {
          ctx.textAlign = "center";
          ctx.textBaseline = "middle";
          ctx.font = `${Math.round(half * 1.6)}px serif`;
          ctx.fillText(emo, cx, cy);
        }
      }
      // Cross-zone seams and grid contacts both need a boundary signal. Grid
      // contacts get small directional notches so they do not read as portals.
      const crossZoneGrid = r.xz ?? [];
      if (r.se.length > 0 || crossZoneGrid.length > 0) {
        ctx.lineWidth = Math.max(1, 2 * S);
        ctx.strokeStyle = COL.amber;
        ctx.beginPath();
        ctx.arc(cx, cy, half + 3 * S, 0, 7);
        ctx.stroke();
      }
      if (crossZoneGrid.length > 0) {
        ctx.lineWidth = Math.max(1, 2 * S);
        ctx.strokeStyle = COL.amber;
        for (const contact of crossZoneGrid) {
          this.paintCrossZoneNotch(cx, cy, half, S, contact.dir, contact.p);
        }
      }
      if (!r.p && (up || dn)) {
        ctx.fillStyle = COL.ink;
        ctx.font = `${8 * S}px monospace`;
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        ctx.fillText(up && dn ? "⇅" : up ? "▲" : "▼", cx, cy);
      }
    }

    this.paintScannedFrontier(S, half, AL);
    this.paintMarker(S, half, AL);
  }

  private paintScannedFrontier(S: number, half: number, AL: number): void {
    const frontier = this.frontier;
    if (!frontier || frontier.zone !== this.zoneName) return;
    const room = this.findRoom(frontier.x, frontier.y, frontier.l);
    const level = room ? displayLevel(room) : frontier.l;
    if (level !== AL) return;
    const anchor = room ?? ({ x: frontier.x, y: frontier.y } as SlimRoom);
    const cx = screenX(anchor) * CELL * S + this.view.ox;
    const cy = screenY(anchor) * CELL * S + this.view.oy;
    const ctx = this.ctx;
    ctx.save();
    ctx.strokeStyle = "rgba(84,232,136,.9)";
    ctx.fillStyle = COL.ink;
    ctx.lineWidth = Math.max(1.5, 2.5 * S);
    ctx.font = `${Math.max(9, Math.round(9 * S))}px monospace`;
    ctx.textBaseline = "middle";
    for (const item of frontier.hints) {
      const v = DIR_SCREEN_VEC[item.dir];
      if (!v) {
        if (item.dir !== "U" && item.dir !== "D") continue;
        const sign = item.dir === "U" ? -1 : 1;
        const y = cy + sign * (half + 12 * S);
		ctx.fillStyle = "rgba(84,232,136,.9)";
        ctx.textAlign = "left";
        ctx.fillText(`${item.dir === "U" ? "↑" : "↓"} ? ${shortScanHint(item.hint)}`, cx + half + 3 * S, y);
        continue;
      }
      const [vx, vy] = v;
      const px = -vy, py = vx;
      const targetX = cx + vx * CELL * S;
      const targetY = cy + vy * CELL * S;
      const nearX = targetX - vx * half;
      const nearY = targetY - vy * half;

      // Extend the ordinary stub past the marker ring to the near face of the
      // unknown cell. Then draw only the NEAR HALF of its outline: a full square
      // would falsely claim that the mapper knows a room already exists there.
      ctx.beginPath();
      ctx.moveTo(cx, cy);
      ctx.lineTo(nearX, nearY);
      ctx.stroke();
      ctx.beginPath();
      ctx.moveTo(nearX + px * half, nearY + py * half);
      ctx.lineTo(nearX - px * half, nearY - py * half);
      ctx.lineTo(nearX - px * half + vx * half, nearY - py * half + vy * half);
      ctx.moveTo(nearX + px * half, nearY + py * half);
      ctx.lineTo(nearX + px * half + vx * half, nearY + py * half + vy * half);
      ctx.stroke();

      const label = `? ${shortScanHint(item.hint)}`;
      if (vx > 0) {
        ctx.textAlign = "left";
        ctx.fillText(label, targetX + 3 * S, targetY);
      } else if (vx < 0) {
        ctx.textAlign = "right";
        ctx.fillText(label, targetX - 3 * S, targetY);
      } else {
        ctx.textAlign = "center";
        ctx.fillText(label, targetX, targetY + vy * (half + 8 * S));
      }
    }
    ctx.restore();
  }

  private paintCrossZoneNotch(
    cx: number,
    cy: number,
    half: number,
    S: number,
    dir: string,
    passable: boolean,
  ): void {
    const span = Math.max(3, 4 * S);
    const outer = half + 5 * S;
    const ctx = this.ctx;
    let bx = cx;
    let by = cy;
    ctx.strokeStyle = COL.amber;
    ctx.beginPath();
    switch (dir) {
      case "N": case "U":
        by -= outer;
        ctx.moveTo(cx - span, by); ctx.lineTo(cx + span, by);
        break;
      case "S": case "D":
        by += outer;
        ctx.moveTo(cx - span, by); ctx.lineTo(cx + span, by);
        break;
      case "E":
        bx += outer;
        ctx.moveTo(bx, cy - span); ctx.lineTo(bx, cy + span);
        break;
      case "W":
        bx -= outer;
        ctx.moveTo(bx, cy - span); ctx.lineTo(bx, cy + span);
        break;
      default: return;
    }
    ctx.stroke();
    if (!passable) {
      const cross = Math.max(2, 2.5 * S);
      ctx.strokeStyle = COL.red;
      ctx.beginPath();
      ctx.moveTo(bx - cross, by - cross);
      ctx.lineTo(bx + cross, by + cross);
      ctx.moveTo(bx + cross, by - cross);
      ctx.lineTo(bx - cross, by + cross);
      ctx.stroke();
    }
  }

  private paintSelection(S: number, half: number, AL: number): void {
    const ctx = this.ctx;
    if (this.selected.size > 0) {
      ctx.save();
      ctx.strokeStyle = "#f0b64e";
      ctx.lineWidth = Math.max(1.5, 2 * S);
      ctx.setLineDash([]);
      for (const room of this.rooms) {
        if (displayLevel(room) !== AL || !this.selected.has(this.key(room))) continue;
        const cx = this.sx(room);
        const cy = this.sy(room);
        if (cx < -half || cy < -half || cx > this.canvas.clientWidth + half || cy > this.canvas.clientHeight + half) continue;
        this.roundRect(cx - half - 3 * S, cy - half - 3 * S, (half + 3 * S) * 2, (half + 3 * S) * 2, 3 * S);
        ctx.stroke();
      }
      ctx.restore();
    }
    if (!this.selecting || !this.selectStart || !this.selectEnd) return;
    const x = Math.min(this.selectStart.x, this.selectEnd.x);
    const y = Math.min(this.selectStart.y, this.selectEnd.y);
    ctx.save();
    ctx.fillStyle = "rgba(240,182,78,.10)";
    ctx.strokeStyle = "rgba(240,182,78,.9)";
    ctx.lineWidth = 1;
    ctx.setLineDash([4, 3]);
    ctx.fillRect(x, y, Math.abs(this.selectEnd.x - this.selectStart.x), Math.abs(this.selectEnd.y - this.selectStart.y));
    ctx.strokeRect(x, y, Math.abs(this.selectEnd.x - this.selectStart.x), Math.abs(this.selectEnd.y - this.selectStart.y));
    ctx.restore();
  }

  // "You are here" marker: ring in the confidence color, drawn only when the
  // marker is on the active zone+floor. Always LAST so it sits on top of both the
  // rooms and the candidate cell — including on an empty zone, where the room
  // drawing bails out early but the player is still somewhere.
  private paintMarker(S: number, half: number, AL: number): void {
    if (!this.marker) return;
    const room = this.findRoom(this.marker.x, this.marker.y, this.marker.l);
    const markerLevel = room ? displayLevel(room) : this.marker.l;
    if (markerLevel !== AL) return;
    const ctx = this.ctx;
    const fake = room ?? ({ x: this.marker.x, y: this.marker.y } as SlimRoom);
    const cx = screenX(fake) * CELL * S + this.view.ox;
    const cy = screenY(fake) * CELL * S + this.view.oy;
    ctx.strokeStyle = this.marker.color;
    ctx.lineWidth = Math.max(2, 2.5 * S);
    ctx.beginPath();
    ctx.arc(cx, cy, half + 6 * S, 0, 7);
    ctx.stroke();
    // Filled core dot so it reads even at low zoom.
    ctx.fillStyle = this.marker.color;
    ctx.beginPath();
    ctx.arc(cx, cy, Math.max(2, 3 * S), 0, 7);
    ctx.fill();
  }

  // Draw the candidate cell: dashed square in the confidence colour plus the
  // live room name underneath when the pane knows one. setLineDash is reset
  // afterwards so nothing else in the frame inherits the dash.
  private paintGhost(g: GhostCell, S: number, half: number): void {
    const ctx = this.ctx;
    const cx = screenX(g) * CELL * S + this.view.ox;
    const cy = screenY(g) * CELL * S + this.view.oy;
    ctx.save();
    ctx.setLineDash([Math.max(2, 3 * S), Math.max(2, 3 * S)]);
    ctx.lineWidth = Math.max(1, 1.5 * S);
    ctx.strokeStyle = g.color;
    this.roundRect(cx - half, cy - half, half * 2, half * 2, 3 * S);
    ctx.stroke();
    ctx.restore();
    if (g.label && half > 4) {
      ctx.save();
      ctx.fillStyle = g.color;
      ctx.font = `${Math.max(9, Math.round(9 * S))}px monospace`;
      ctx.textAlign = "center";
      ctx.textBaseline = "top";
      ctx.fillText(g.label, cx, cy + half + 3 * S);
      ctx.restore();
    }
  }

  // The HAND-DRAWN layer: hidden exits and negations, drawn so they cannot be
  // mistaken for what the MUD said. Markup the player cannot see is markup the
  // player cannot take back, so this is a requirement, not decoration.
  //
  //   hidden exit — a DASHED AMBER stub (the ordinary stubs are solid green, and
  //                 amber already means "hand-made / cross-zone" in this pane);
  //   negation    — a RED BAR across the face, i.e. a wall drawn where the map
  //                 would otherwise have carried you through.
  //
  // Both are drawn per OUTGOING face at half a cell, so the two directed halves of
  // one boundary meet in the middle: a passage open one way and walled the other
  // reads as half amber, half red, which is exactly what it is.
  //
  // U and D have no direction vector on a floor plan, so a mark on them cannot be
  // drawn as a stub. It is counted instead and shown as a corner badge, because
  // "invisible" would mean "unremovable" — the cell menu is where those are taken
  // back (MapPaneController.openCellMenu).
  private paintHandDrawn(S: number, AL: number): void {
    const ctx = this.ctx;
    const reach = CELL * S * 0.5;
    ctx.save();
    for (const r of this.rooms) {
      if (displayLevel(r) !== AL) continue;
      if (r.m.length === 0 && r.mb.length === 0) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);

      ctx.lineWidth = Math.max(2, 3 * S);
      ctx.strokeStyle = COL.amber;
      ctx.setLineDash([Math.max(2, 3 * S), Math.max(2, 3 * S)]);
      for (const d of r.m) {
        const v = DIR_SCREEN_VEC[d];
        if (!v) continue;
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(cx + v[0] * reach, cy + v[1] * reach);
        ctx.stroke();
      }
      ctx.setLineDash([]);

      ctx.lineWidth = Math.max(2, 3 * S);
      ctx.strokeStyle = COL.red;
      for (const d of r.mb) {
        const v = DIR_SCREEN_VEC[d];
        if (!v) continue;
        // The bar sits ON the boundary, across the direction of travel.
        const bx = cx + v[0] * reach * 0.8;
        const by = cy + v[1] * reach * 0.8;
        const half = Math.max(3, ROOM * S * 0.4);
        ctx.beginPath();
        ctx.moveTo(bx - v[1] * half, by - v[0] * half);
        ctx.lineTo(bx + v[1] * half, by + v[0] * half);
        ctx.stroke();
      }

      // Vertical marks: a badge in the cell's top-left corner, amber when any of
      // them is a passage, red when they are all denials.
      const vert = countVerticalMarks(r);
      if (vert.total > 0 && ROOM * S > 6) {
        ctx.fillStyle = vert.open > 0 ? COL.amber : COL.red;
        ctx.beginPath();
        ctx.arc(
          cx - (ROOM * S) / 2 + 2 * S,
          cy - (ROOM * S) / 2 + 2 * S,
          Math.max(1.5, 2.2 * S),
          0,
          7,
        );
        ctx.fill();
      }
    }
    ctx.restore();
  }

  // Hit-test the BOUNDARY between two grid-adjacent rooms of the active floor —
  // the edge editor's click target. Two problems it has to solve at once:
  //
  //  1. THERE IS NOTHING DRAWN TO CLICK when a boundary carries no edge, and
  //     "mark a passage that does not exist yet" is the main thing the editor is
  //     for. So the target is the boundary itself (the gutter between two room
  //     squares), not any painted stub: every pair of adjacent rooms is clickable
  //     whether or not anything connects them.
  //  2. IT MUST NOT STEAL A CELL CLICK. The cell context menu already owns the
  //     room squares. This runs only AFTER roomAt() misses, so a room always wins
  //     its own pixels and the edge takes only the gap.
  //
  // The band is generous (it may overlap the squares — harmless, rooms are tested
  // first), so in practice the target is "the gutter", whatever is left of it.
  //
  // THE NEAREST BOUNDARY WINS, not the first one scanned. The bands of neighbouring
  // boundaries overlap once the cell pitch drops under roughly twice the band
  // width, and returning the first match in `this.rooms` order then opened the
  // editor for a DIFFERENT edge than the one under the cursor — a silent, confident
  // wrong answer rather than a miss. Ranking by distance to the boundary's midpoint
  // makes the result depend on the pointer instead of on room order, so the answer
  // stays right (if increasingly fiddly) all the way down to the wheel's minimum
  // zoom; below a pitch of ~15 px (view scale 0.5) the neighbouring boundary is
  // only a few pixels away, so aim carefully or zoom in.
  //
  // Each boundary is owned by exactly one of its two cells (only S and E are
  // probed, which are the +x and +y steps) so the popup's side order is stable no
  // matter which half of the gutter was clicked.
  //
  // U/D have no boundary on a 2-D floor plan and are therefore not reachable here;
  // the cell menu carries their marks instead.
  private edgeAt(px: number, py: number): { room: SlimRoom; dir: string } | null {
    const S = this.view.s;
    const along = Math.max(5, ((CELL - ROOM) * S) / 2);
    const across = Math.max(6, (ROOM * S) / 2);
    let best: { room: SlimRoom; dir: string } | null = null;
    let bestD2 = Infinity;
    for (const r of this.rooms) {
      if (displayLevel(r) !== this.level) continue;
      for (const dir of ["S", "E"]) {
        const d = stepDelta(dir);
        if (!d) continue;
        const nb = this.findRoom(r.x + d.dx, r.y + d.dy, r.l + d.dl);
        if (!nb) continue;
        const mx = (this.sx(r) + this.sx(nb)) / 2;
        const my = (this.sy(r) + this.sy(nb)) / 2;
        // S runs down the screen (dy), E runs across it (dx).
        const dx = Math.abs(px - mx);
        const dy = Math.abs(py - my);
        const hit =
          dir === "S" ? dy <= along && dx <= across : dx <= along && dy <= across;
        if (!hit) continue;
        const d2 = dx * dx + dy * dy;
        if (d2 < bestD2) {
          bestD2 = d2;
          best = { room: r, dir };
        }
      }
    }
    return best;
  }

  private roundRect(
    x: number,
    y: number,
    w: number,
    h: number,
    r: number,
  ): void {
    const ctx = this.ctx;
    r = Math.max(0, Math.min(r, w / 2, h / 2));
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  // Half-extent of a cell's CLICK box. The room square is ROOM wide inside a CELL
  // pitch, and the padding that makes a small square easier to hit is capped so
  // the boxes of two adjacent cells can never meet: the gutter between them is the
  // edge editor's only target, and a flat +2 px closes it completely once the
  // pitch drops under ~16 px. Rooms still win every pixel they claim — the cap
  // only stops them claiming the gap as well.
  private hitHalf(): number {
    const S = this.view.s;
    const gap = (CELL - ROOM) * S;
    return (ROOM * S) / 2 + Math.max(0, Math.min(2, gap / 2 - 1));
  }

  private roomAt(px: number, py: number): SlimRoom | null {
    const half = this.hitHalf();
    for (const r of this.rooms) {
      if (displayLevel(r) !== this.level) continue;
      if (
        Math.abs(px - this.sx(r)) <= half &&
        Math.abs(py - this.sy(r)) <= half
      )
        return r;
    }
    return null;
  }

  // Public hit-test for callers that need the room under a canvas-local point
  // using the renderer's current view (e.g. the "I'm here" anchor picker). The
  // `rooms` argument is accepted for API symmetry but the renderer's own loaded
  // rooms are authoritative.
  pick(px: number, py: number, _rooms?: SlimRoom[]): SlimRoom | null {
    return this.roomAt(px, py);
  }

  // Hit-test the candidate cell. Deliberately SEPARATE from roomAt: that
  // function's contract is "a real room of the loaded zone", and loosening it
  // would leak a phantom room into hover tips, seam handling and pick(). Only
  // called after roomAt misses.
  private ghostAt(px: number, py: number): GhostCell | null {
    const g = this.ghost;
    if (!g || g.l !== this.level) return null;
    const S = this.view.s;
    const halfHit = (ROOM * S) / 2 + 2;
    const cx = screenX(g) * CELL * S + this.view.ox;
    const cy = screenY(g) * CELL * S + this.view.oy;
    if (Math.abs(px - cx) <= halfHit && Math.abs(py - cy) <= halfHit) return g;
    return null;
  }

  private attach(): void {
    const cv = this.canvas;
    const signal = this.listeners.signal;
    const begin = (e: MouseEvent | PointerEvent) => {
      // Primary drag selects in selection mode. Middle drag always pans, which
      // keeps a large selection navigable without leaving the mode.
      if (e.button !== 0 && e.button !== 1) return;
      const selectionGesture = this.selectionMode && e.button === 0;
      this.moved = false;
      if (selectionGesture) {
        const rect = cv.getBoundingClientRect();
        this.selecting = true;
        this.selectStart = { x: e.clientX - rect.left, y: e.clientY - rect.top };
        this.selectEnd = this.selectStart;
        this.render();
      } else {
        this.dragging = true;
        this.lastX = e.clientX;
        this.lastY = e.clientY;
        cv.classList.add("drag");
      }
    };
    const move = (e: MouseEvent | PointerEvent) => {
      if (this.selecting) {
        const rect = cv.getBoundingClientRect();
        this.selectEnd = { x: e.clientX - rect.left, y: e.clientY - rect.top };
        this.render();
        return;
      }
      if (!this.dragging) return;
      const dx = e.clientX - this.lastX;
      const dy = e.clientY - this.lastY;
      if (Math.abs(dx) + Math.abs(dy) > 2) this.moved = true;
      this.view.ox += dx;
      this.view.oy += dy;
      this.lastX = e.clientX;
      this.lastY = e.clientY;
      this.render();
    };
    const activate = (e: MouseEvent | PointerEvent) => {
      if (this.moved) return;
      const rect = cv.getBoundingClientRect();
      const px = e.clientX - rect.left;
      const py = e.clientY - rect.top;
      const room = this.roomAt(px, py);
      if (this.selectionMode) {
        if (room) this.toggleSelected(room);
        return;
      }
      if (room) {
        this.onRoomClick?.(room, e.clientX, e.clientY);
        return;
      }
      const ghost = this.ghostAt(px, py);
      if (ghost) {
        this.onGhostClick?.(ghost, e.clientX, e.clientY);
        return;
      }
      const edge = this.edgeAt(px, py);
      if (edge) this.onEdgeClick?.(edge.room, edge.dir, e.clientX, e.clientY);
    };
    const finish = (e: MouseEvent | PointerEvent, cancelled = false) => {
      if (this.selecting) {
        const rect = cv.getBoundingClientRect();
        const end = { x: e.clientX - rect.left, y: e.clientY - rect.top };
        const start = this.selectStart!;
        const isDrag = !cancelled && Math.abs(end.x - start.x) + Math.abs(end.y - start.y) > 4;
        this.selecting = false;
        this.selectStart = null;
        this.selectEnd = null;
        if (isDrag) this.selectRect(start, end, e.shiftKey);
        this.moved = isDrag;
        this.render();
      }
      this.dragging = false;
      cv.classList.remove("drag");
    };

    if (typeof PointerEvent !== "undefined") {
      cv.addEventListener("pointerdown", (e) => {
        if (e.pointerType === "touch") {
          if (this.touchPointers.size >= 2) return;
          this.touchPointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
          cv.setPointerCapture?.(e.pointerId);
          e.preventDefault();
          if (this.touchPointers.size === 2) {
            const [a, b] = [...this.touchPointers.values()];
            const rect = cv.getBoundingClientRect();
            this.pinch = {
              distance: Math.max(1, Math.hypot(b.x - a.x, b.y - a.y)),
              x: (a.x + b.x) / 2 - rect.left,
              y: (a.y + b.y) / 2 - rect.top,
              view: { ...this.view },
            };
            this.gestureHadPinch = true;
            this.activePointerID = null;
            this.selecting = false;
            this.selectStart = null;
            this.selectEnd = null;
            this.dragging = false;
            cv.classList.remove("drag");
            this.moved = true;
            this.render();
            return;
          }
        } else if (!e.isPrimary) {
          return;
        }
        if (this.activePointerID !== null || (e.button !== 0 && e.button !== 1)) return;
        this.activePointerID = e.pointerId;
        this.activePointerButton = e.button;
        cv.setPointerCapture?.(e.pointerId);
        if (e.pointerType !== "mouse") e.preventDefault();
        begin(e);
      }, { signal });
      cv.addEventListener("pointermove", (e) => {
        if (e.pointerType === "touch" && this.touchPointers.has(e.pointerId)) {
          this.touchPointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
          if (this.pinch && this.touchPointers.size === 2) {
            const [a, b] = [...this.touchPointers.values()];
            const rect = cv.getBoundingClientRect();
            const x = (a.x + b.x) / 2 - rect.left;
            const y = (a.y + b.y) / 2 - rect.top;
            const scale = Math.hypot(b.x - a.x, b.y - a.y) / this.pinch.distance;
            const nextScale = Math.max(0.2, Math.min(6, this.pinch.view.s * scale));
            const worldX = (this.pinch.x - this.pinch.view.ox) / this.pinch.view.s;
            const worldY = (this.pinch.y - this.pinch.view.oy) / this.pinch.view.s;
            this.view.s = nextScale;
            this.view.ox = x - worldX * nextScale;
            this.view.oy = y - worldY * nextScale;
            this.moved = true;
            this.render();
            e.preventDefault();
            return;
          }
        }
        if (e.pointerId !== this.activePointerID) return;
        if (e.pointerType !== "mouse") e.preventDefault();
        move(e);
      }, { signal });
      const endPointer = (e: PointerEvent, cancelled: boolean) => {
        if (e.pointerType === "touch" && this.touchPointers.has(e.pointerId)) {
          this.touchPointers.delete(e.pointerId);
          if (this.gestureHadPinch) {
            this.pinch = null;
            this.activePointerID = null;
            this.selecting = false;
            this.selectStart = null;
            this.selectEnd = null;
            this.dragging = false;
            if (this.touchPointers.size === 0) this.gestureHadPinch = false;
            if (cv.hasPointerCapture?.(e.pointerId)) cv.releasePointerCapture?.(e.pointerId);
            return;
          }
        }
        if (e.pointerId !== this.activePointerID) return;
        const button = this.activePointerButton;
        finish(e, cancelled);
        if (!cancelled && button === 0) activate(e);
        this.activePointerID = null;
        if (cv.hasPointerCapture?.(e.pointerId)) cv.releasePointerCapture?.(e.pointerId);
      };
      cv.addEventListener("pointerup", (e) => endPointer(e, false), { signal });
      cv.addEventListener("pointercancel", (e) => endPointer(e, true), { signal });
      cv.addEventListener("lostpointercapture", (e) => {
        this.touchPointers.delete(e.pointerId);
        if (this.gestureHadPinch) {
          this.pinch = null;
          if (this.touchPointers.size === 0) this.gestureHadPinch = false;
        }
        if (e.pointerId !== this.activePointerID) return;
        finish(e, true);
        this.activePointerID = null;
      }, { signal });
    } else {
      cv.addEventListener("mousedown", begin, { signal });
      window.addEventListener("mouseup", (e) => finish(e), { signal });
      window.addEventListener("mousemove", move, { signal });
      cv.addEventListener("click", activate, { signal });
    }
    cv.addEventListener(
      "mousemove",
      (e) => {
        const rect = cv.getBoundingClientRect();
        const px = e.clientX - rect.left;
        const py = e.clientY - rect.top;
        const r = this.roomAt(px, py);
        this.onRoomHover?.(r, e.clientX, e.clientY);
      },
      { signal },
    );
    cv.addEventListener("mouseleave", () => this.onRoomHover?.(null, 0, 0), {
      signal,
    });
    cv.addEventListener(
      "dblclick",
      () => {
        this.fit();
        this.render();
      },
      { signal },
    );
    cv.addEventListener(
      "wheel",
      (e) => {
        e.preventDefault();
        const rect = cv.getBoundingClientRect();
        const px = e.clientX - rect.left;
        const py = e.clientY - rect.top;
        const f = e.deltaY < 0 ? 1.12 : 0.89;
        const ns = Math.max(0.2, Math.min(6, this.view.s * f));
        this.view.ox = px - (px - this.view.ox) * (ns / this.view.s);
        this.view.oy = py - (py - this.view.oy) * (ns / this.view.s);
        this.view.s = ns;
        this.render();
      },
      { passive: false, signal },
    );

    // Watch the canvas for any size change after mount. A ResizeObserver fires
    // on the initial observe too, which doubles as the "size settled after
    // layout" measurement (guarding against a 0-height first measure in
    // onMounted's rAF). resize() no-ops when the backing store already matches,
    // so redundant ticks are cheap; when it does resize it repaints (clearing
    // the full backing store) so no stale band can survive a shrink.
    if (typeof ResizeObserver !== "undefined") {
      this.resizeObserver = new ResizeObserver(() => {
        if (this.destroyed) return;
        // resize() itself schedules a render() on an actual change; on a no-op
        // change (e.g. only DPR-irrelevant reflow) we still repaint defensively.
        if (!this.resize()) this.render();
      });
      this.resizeObserver.observe(cv);
    }
  }

  private toggleSelected(room: SlimRoom): void {
    const key = this.key(room);
    if (this.selected.has(key)) this.selected.delete(key);
    else if (this.selected.size < MAX_SELECTION) this.selected.add(key);
    this.emitSelection();
    this.render();
  }

  private selectRect(start: { x: number; y: number }, end: { x: number; y: number }, toggle: boolean): void {
    const left = Math.min(start.x, end.x), right = Math.max(start.x, end.x);
    const top = Math.min(start.y, end.y), bottom = Math.max(start.y, end.y);
    if (!toggle) this.selected.clear();
    for (const room of this.rooms) {
      if (displayLevel(room) !== this.level) continue;
      const x = this.sx(room), y = this.sy(room);
      if (x < left || x > right || y < top || y > bottom) continue;
      const key = this.key(room);
      if (toggle && this.selected.has(key)) this.selected.delete(key);
      else if (this.selected.size < MAX_SELECTION) this.selected.add(key);
    }
    this.emitSelection();
  }
}

// Structural equality for the ghost so setGhost can skip redundant repaints.
function sameGhost(a: GhostCell | null, b: GhostCell | null): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return (
    a.x === b.x &&
    a.y === b.y &&
    a.l === b.l &&
    a.color === b.color &&
    a.label === b.label
  );
}

function sameFrontier(a: ScannedFrontier | null, b: ScannedFrontier | null): boolean {
  if (a === b) return true;
  if (!a || !b || a.zone !== b.zone || a.x !== b.x || a.y !== b.y || a.l !== b.l || a.hints.length !== b.hints.length) return false;
  return a.hints.every((hint, i) => hint.dir === b.hints[i]?.dir && hint.hint === b.hints[i]?.hint);
}

function shortScanHint(hint: string): string {
  const text = hint.trim();
  return text.length > 28 ? `${text.slice(0, 27)}…` : text;
}

export { EMOJI };

// countVerticalMarks counts a cell's hand-drawn U/D marks. They have no direction
// vector on a floor plan, so they cannot be drawn as stubs; the pane shows them as
// a badge and offers them in the cell menu, because a mark the player cannot see
// is a mark the player cannot remove.
export function countVerticalMarks(r: SlimRoom): {
  total: number;
  open: number;
  blocked: number;
} {
  let open = 0;
  let blocked = 0;
  for (const d of ["U", "D"]) {
    if (r.m.includes(d)) open++;
    if (r.mb.includes(d)) blocked++;
  }
  return { total: open + blocked, open, blocked };
}
