import {
  computeBounds,
  defaultLevel,
  DIR_SCREEN_VEC,
  firstSeam,
  levelsOf,
  screenX,
  screenY,
  type Bounds,
} from './geometry';
import type { SlimRoom } from './types';

// Ported from tmp/mapper-artifacts/webmap/krynn_map_template.html (canvas
// renderer prototype), typed and made instance-based (no global DOM). One
// MapRenderer owns one <canvas>, its view transform, the active zone's rooms,
// the active floor, and an optional "you are here" marker. Read-only. Room
// thumbnails/images (phase 4) are OUT of scope — a clean seam is left in draw().

const CELL = 30;
const ROOM = 15;

// imageindex → emoji glyph, from the prototype.
const EMOJI: Record<number, string> = {
  1: '🌑',
  3: '👹',
  4: '🛠️',
  5: '⚔️',
  6: '🔮',
  8: '🔨',
  9: '📦',
  11: '🥷',
  12: '⛪',
  14: '🛡️',
  15: '💎',
  17: '🍺',
  18: '⛲',
  19: '🏦',
  20: '🌲',
};

const COL = {
  line: '#1b2a20',
  phos: '#54e888',
  amber: '#f0b64e',
  red: '#ff5d5d',
  ink: '#cfe6d6',
  ground: '#090c0a',
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

export type RoomClickHandler = (room: SlimRoom) => void;

export class MapRenderer {
  private ctx: CanvasRenderingContext2D;
  private rooms: SlimRoom[] = [];
  private coords = new Set<string>();
  private bounds: Bounds = { minx: 0, miny: 0, maxx: 0, maxy: 0 };
  private levels: number[] = [];
  level = 0;
  zoneName = '';
  private view: View = { s: 1, ox: 0, oy: 0 };
  private marker: Marker | null = null;
  private dragging = false;
  private lastX = 0;
  private lastY = 0;
  private moved = false;
  // One AbortController owns every event listener (canvas + window) so destroy()
  // can tear all of them down in one call — no leaked zombie window handlers.
  private listeners = new AbortController();
  // rAF coalescing: repeated render() calls within a frame collapse to one paint.
  private renderScheduled = false;
  private destroyed = false;

  // Callbacks the pane controller wires up.
  onSeamClick?: (target: ReturnType<typeof firstSeam>, room: SlimRoom) => void;
  onRoomHover?: (room: SlimRoom | null, clientX: number, clientY: number) => void;

  constructor(public readonly canvas: HTMLCanvasElement) {
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('2d canvas context unavailable');
    this.ctx = ctx;
    this.attach();
  }

  // Remove every event listener and stop future paints. Idempotent. Called by
  // MapPaneController.dispose() on split/close/rebuild/buffer-switch.
  destroy(): void {
    this.destroyed = true;
    this.listeners.abort();
  }

  // Load a zone's rooms and reset the view to a readable default focus.
  setZone(zoneName: string, rooms: SlimRoom[]): void {
    this.zoneName = zoneName;
    this.rooms = rooms;
    this.coords = new Set(rooms.map((r) => this.key(r)));
    this.bounds = computeBounds(rooms);
    this.levels = levelsOf(rooms);
    this.level = defaultLevel(rooms);
    this.focusDefault();
    this.render();
  }

  levelList(): number[] {
    return this.levels;
  }

  setLevel(l: number): void {
    if (l === this.level) return;
    this.level = l;
    this.render();
  }

  // Place/clear the "you are here" marker. Re-centers only via followTo().
  setMarker(m: Marker | null): void {
    this.marker = m;
    this.render();
  }

  // Center the view on a game coord and (optionally) switch floors, used by both
  // seam jumps and follow-player. Assumes the coord belongs to the loaded zone.
  centerOn(x: number, y: number, l: number): void {
    if (l !== this.level && this.levels.includes(l)) {
      this.level = l;
    }
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    this.view.ox = w / 2 - y * CELL * this.view.s;
    this.view.oy = h / 2 - x * CELL * this.view.s;
    this.render();
  }

  findRoom(x: number, y: number, l: number): SlimRoom | undefined {
    return this.rooms.find((r) => r.x === x && r.y === y && r.l === l);
  }

  // Resize backing store to the element's CSS size (call on mount/resize).
  resize(): void {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.canvas.width = Math.max(1, this.canvas.clientWidth * dpr);
    this.canvas.height = Math.max(1, this.canvas.clientHeight * dpr);
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.render();
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
    this.view.s = Math.max(fitS, Math.min(1.8, 40 / CELL));
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
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    ctx.clearRect(0, 0, w, h);
    if (this.rooms.length === 0) return;
    const S = this.view.s;
    const half = (ROOM * S) / 2;
    const AL = this.level;

    // Ghost rooms of inactive levels (faint underlay).
    ctx.fillStyle = 'rgba(47,138,82,.12)';
    for (const r of this.rooms) {
      if (r.l === AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      this.roundRect(cx - half, cy - half, half * 2, half * 2, 2 * S);
      ctx.fill();
    }

    // Exit stubs (active level).
    ctx.lineWidth = Math.max(1, 1.4 * S);
    ctx.strokeStyle = COL.line;
    for (const r of this.rooms) {
      if (r.l !== AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      for (const d of r.e) {
        const v = DIR_SCREEN_VEC[d];
        if (!v) continue;
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(cx + v[0] * CELL * S * 0.5, cy + v[1] * CELL * S * 0.5);
        ctx.stroke();
      }
    }

    // Rooms (active level).
    for (const r of this.rooms) {
      if (r.l !== AL) continue;
      const cx = this.sx(r);
      const cy = this.sy(r);
      const up = r.e.includes('U');
      const dn = r.e.includes('D');

      if (r.p) {
        // Narrow corridor cell: orientation from neighbour geometry (pipe cells
        // have empty edirs). screenX=y, screenY=x.
        const co = this.coords;
        const hL = co.has(`${r.x},${r.y - 1},${r.l}`);
        const hR = co.has(`${r.x},${r.y + 1},${r.l}`);
        const vU = co.has(`${r.x - 1},${r.y},${r.l}`);
        const vD = co.has(`${r.x + 1},${r.y},${r.l}`);
        const horz = hL || hR;
        const vert = vU || vD;
        ctx.fillStyle = r.s ? 'rgba(255,93,93,.7)' : 'rgba(47,138,82,.6)';
        const thin = Math.max(2, 4 * S);
        const span = CELL * S;
        let axis: 'h' | 'v' | null;
        if (horz && vert) axis = hL && hR ? 'h' : vU && vD ? 'v' : 'h';
        else if (horz) axis = 'h';
        else if (vert) axis = 'v';
        else axis = null;
        if (axis === 'h') {
          this.roundRect(cx - span / 2, cy - thin / 2, span, thin, 1 * S);
          ctx.fill();
        } else if (axis === 'v') {
          this.roundRect(cx - thin / 2, cy - span / 2, thin, span, 1 * S);
          ctx.fill();
        } else {
          ctx.beginPath();
          ctx.arc(cx, cy, Math.max(1.5, 2 * S), 0, 7);
          ctx.fill();
        }
        continue;
      }

      // Named room square. (Room thumbnail image layer — phase 4 — would draw
      // here in place of the flat fill; the seam is intentionally left open.)
      const emo = r.i != null ? EMOJI[r.i] : undefined;
      ctx.fillStyle = r.s ? 'rgba(255,93,93,.9)' : emo ? 'rgba(47,138,82,.6)' : 'rgba(47,138,82,.85)';
      this.roundRect(cx - half, cy - half, half * 2, half * 2, 3 * S);
      ctx.fill();
      if (emo && half > 5) {
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.font = `${Math.round(half * 1.6)}px serif`;
        ctx.fillText(emo, cx, cy);
      }
      // Seam ring (room advertises a cross-zone seam).
      if (r.a.length > 0) {
        ctx.lineWidth = Math.max(1, 2 * S);
        ctx.strokeStyle = COL.amber;
        ctx.beginPath();
        ctx.arc(cx, cy, half + 3 * S, 0, 7);
        ctx.stroke();
      }
      if (up || dn) {
        ctx.fillStyle = COL.ink;
        ctx.font = `${8 * S}px monospace`;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillText(up && dn ? '⇅' : up ? '▲' : '▼', cx, cy);
      }
    }

    // "You are here" marker: pulsing ring in the confidence color, drawn only
    // when the marker is on the active zone+floor.
    if (this.marker && this.marker.l === AL) {
      const fake = { x: this.marker.x, y: this.marker.y } as SlimRoom;
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
  }

  private roundRect(x: number, y: number, w: number, h: number, r: number): void {
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

  private roomAt(px: number, py: number): SlimRoom | null {
    const S = this.view.s;
    const half = (ROOM * S) / 2 + 2;
    for (const r of this.rooms) {
      if (r.l !== this.level) continue;
      if (Math.abs(px - this.sx(r)) <= half && Math.abs(py - this.sy(r)) <= half) return r;
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

  private attach(): void {
    const cv = this.canvas;
    const signal = this.listeners.signal;
    cv.addEventListener(
      'mousedown',
      (e) => {
        this.dragging = true;
        this.moved = false;
        this.lastX = e.clientX;
        this.lastY = e.clientY;
        cv.classList.add('drag');
      },
      { signal },
    );
    window.addEventListener(
      'mouseup',
      () => {
        this.dragging = false;
        cv.classList.remove('drag');
      },
      { signal },
    );
    window.addEventListener(
      'mousemove',
      (e) => {
        if (!this.dragging) return;
        const dx = e.clientX - this.lastX;
        const dy = e.clientY - this.lastY;
        if (Math.abs(dx) + Math.abs(dy) > 2) this.moved = true;
        this.view.ox += dx;
        this.view.oy += dy;
        this.lastX = e.clientX;
        this.lastY = e.clientY;
        this.render();
      },
      { signal },
    );
    cv.addEventListener(
      'mousemove',
      (e) => {
        const rect = cv.getBoundingClientRect();
        const px = e.clientX - rect.left;
        const py = e.clientY - rect.top;
        const r = this.roomAt(px, py);
        this.onRoomHover?.(r, e.clientX, e.clientY);
      },
      { signal },
    );
    cv.addEventListener('mouseleave', () => this.onRoomHover?.(null, 0, 0), { signal });
    cv.addEventListener(
      'click',
      (e) => {
        if (this.moved) return;
        const rect = cv.getBoundingClientRect();
        const px = e.clientX - rect.left;
        const py = e.clientY - rect.top;
        const r = this.roomAt(px, py);
        if (!r) return;
        const seam = firstSeam(r);
        if (seam) {
          this.onSeamClick?.(seam, r);
        }
      },
      { signal },
    );
    cv.addEventListener(
      'dblclick',
      () => {
        this.fit();
        this.render();
      },
      { signal },
    );
    cv.addEventListener(
      'wheel',
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
  }
}

export { EMOJI };
