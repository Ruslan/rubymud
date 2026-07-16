import { fetchWithToken } from "../dom";
import { findZoneName, parseZoneList, resolveSeamTarget } from "./geometry";
import { MapRenderer } from "./renderer";
import type { Confidence, MapSet, PlayerPosition, SlimRoom } from "./types";

// Confidence glyphs and marker ring colors.
const CONF_GLYPH: Record<Confidence, string> = {
  green: "🟢",
  yellow: "🟡",
  red: "🔴",
};
const CONF_COLOR: Record<Confidence, string> = {
  green: "#54e888",
  yellow: "#f0b64e",
  red: "#ff5d5d",
};

// Reserved system buffer name for the map pane (mapper.md §4). Reject as a
// trigger TargetBuffer server-side; here it is the content-type trigger.
export const MAP_BUFFER = "~map";

interface MapPaneConfig {
  sessionID: number | undefined;
  getActiveMapSetID: () => number | null;
  // Populate (replace + focus, do NOT send) the main command input. Optional so
  // the map module stays decoupled from the input DOM — when absent, room-click
  // routing degrades to a status hint instead of touching the input.
  setInputText?: (text: string) => void;
}

// Endpoint response for POST /api/sessions/{id}/map-path.
interface MapPathResponse {
  reachable: boolean;
  directions?: string[];
  summary?: string;
  reason?: string;
  dt?: boolean;
  seams?: number;
  here?: boolean;
}

// MapPaneController owns one map pane: its canvas renderer plus the surrounding
// controls (zone selector, floor stack, follow toggle, confidence badge,
// "I'm here" re-anchor). It fetches slim rooms lazily per zone from the REST
// feed and follows live `room_position` broadcasts. Read-only map render; the
// only writes it issues are the active-map-set change and the anchor POST.
export class MapPaneController {
  readonly root: HTMLElement;
  private renderer: MapRenderer;
  private zoneSelect: HTMLSelectElement;
  private floorStack: HTMLElement;
  private confidenceBadge: HTMLElement;
  private searchInput: HTMLInputElement;
  private tip: HTMLElement;
  private followToggle: HTMLInputElement;
  private anchorBtn: HTMLButtonElement;
  private statusLine: HTMLElement;

  private mapSetID: number | null = null;
  private zoneNames: string[] = [];
  private zoneCache = new Map<string, SlimRoom[]>();
  private currentZone = "";
  private lastPosition: PlayerPosition | null = null;
  private disposed = false;

  constructor(private config: MapPaneConfig) {
    this.root = document.createElement("div");
    this.root.className = "map-pane";

    // --- control bar -------------------------------------------------------
    const bar = document.createElement("div");
    bar.className = "map-pane__bar";

    this.zoneSelect = document.createElement("select");
    this.zoneSelect.className = "map-pane__zone";
    this.zoneSelect.title = "Zone";
    this.zoneSelect.addEventListener("change", () => {
      void this.showZone(this.zoneSelect.value, { resetView: true });
    });
    bar.appendChild(this.zoneSelect);

    this.searchInput = document.createElement("input");
    this.searchInput.type = "search";
    this.searchInput.className = "map-pane__search";
    this.searchInput.placeholder = "Find room…";
    this.searchInput.addEventListener("input", () => void this.onSearch());
    bar.appendChild(this.searchInput);

    const followLabel = document.createElement("label");
    followLabel.className = "map-pane__follow";
    followLabel.title = "Follow the live player position";
    this.followToggle = document.createElement("input");
    this.followToggle.type = "checkbox";
    this.followToggle.checked = true;
    this.followToggle.addEventListener("change", () => {
      if (this.followToggle.checked && this.lastPosition?.valid) {
        void this.applyPosition(this.lastPosition, true);
      }
    });
    followLabel.appendChild(this.followToggle);
    const followText = document.createElement("span");
    followText.textContent = "Follow";
    followLabel.appendChild(followText);
    bar.appendChild(followLabel);

    this.anchorBtn = document.createElement("button");
    this.anchorBtn.type = "button";
    this.anchorBtn.className = "map-pane__anchor";
    this.anchorBtn.textContent = "I'm here";
    this.anchorBtn.title =
      "Re-anchor the tracker to a room you click on the map";
    this.anchorBtn.addEventListener("click", () => this.toggleAnchorMode());
    bar.appendChild(this.anchorBtn);

    this.confidenceBadge = document.createElement("span");
    this.confidenceBadge.className = "map-pane__confidence";
    this.confidenceBadge.title = "Tracker confidence";
    this.confidenceBadge.textContent = "⚪";
    bar.appendChild(this.confidenceBadge);

    this.root.appendChild(bar);

    // --- canvas + overlays -------------------------------------------------
    const stage = document.createElement("div");
    stage.className = "map-pane__stage";
    const canvas = document.createElement("canvas");
    canvas.className = "map-pane__canvas";
    stage.appendChild(canvas);

    this.floorStack = document.createElement("div");
    this.floorStack.className = "map-pane__floors";
    stage.appendChild(this.floorStack);

    this.tip = document.createElement("div");
    this.tip.className = "map-pane__tip";
    this.tip.style.opacity = "0";
    stage.appendChild(this.tip);

    this.statusLine = document.createElement("div");
    this.statusLine.className = "map-pane__status";
    stage.appendChild(this.statusLine);

    this.root.appendChild(stage);

    this.renderer = new MapRenderer(canvas);
    this.renderer.onSeamClick = (seam, room) => {
      if (seam) void this.jumpSeam(seam, room);
    };
    this.renderer.onRoomClick = (room) => void this.pathToInput(room);
    this.renderer.onRoomHover = (room, cx, cy) => this.showTip(room, cx, cy);

    void this.loadMapSet();
  }

  // Called when the pane is (re)mounted into the DOM so the canvas can size.
  onMounted(): void {
    // Defer to next frame so clientWidth/Height are valid.
    requestAnimationFrame(() => {
      if (this.disposed) return;
      this.renderer.resize();
    });
  }

  dispose(): void {
    this.disposed = true;
    this.renderer.destroy();
  }

  // Force a canvas repaint (e.g. on theme refresh).
  render(): void {
    this.renderer.render();
  }

  // Reload everything (e.g. after map_sets_changed / active-set change).
  async reload(): Promise<void> {
    this.zoneCache.clear();
    await this.loadMapSet();
  }

  private async loadMapSet(): Promise<void> {
    const id = this.config.getActiveMapSetID();
    this.mapSetID = id;
    if (id == null) {
      this.setStatus("No active map set. Select one in session settings.");
      this.zoneSelect.innerHTML = "";
      return;
    }
    try {
      const res = await fetchWithToken(`/api/map-sets/${id}/zones`);
      if (!res.ok) throw new Error(await res.text());
      // /api/map-sets/{id}/zones returns [{zone, room_count}], NOT string[].
      const infos = parseZoneList(await res.json());
      this.zoneNames = infos.map((z) => z.zone);
      this.zoneSelect.innerHTML = "";
      for (const info of infos) {
        const opt = document.createElement("option");
        opt.value = info.zone;
        opt.textContent =
          info.room_count > 0 ? `${info.zone} (${info.room_count})` : info.zone;
        this.zoneSelect.appendChild(opt);
      }
      this.setStatus("");
      const start =
        this.lastPosition?.valid &&
        this.zoneNames.includes(this.lastPosition.zone)
          ? this.lastPosition.zone
          : this.zoneNames[0];
      if (start) {
        this.zoneSelect.value = start;
        await this.showZone(start, { resetView: true });
        // Re-apply a pending position now that a zone is loaded.
        if (this.lastPosition?.valid)
          await this.applyPosition(
            this.lastPosition,
            this.followToggle.checked,
          );
      }
    } catch (err) {
      this.setStatus(
        `Failed to load zones: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  private async fetchZone(zone: string): Promise<SlimRoom[]> {
    if (this.zoneCache.has(zone)) return this.zoneCache.get(zone)!;
    if (this.mapSetID == null) return [];
    const res = await fetchWithToken(
      `/api/rooms?map_set=${this.mapSetID}&zone=${encodeURIComponent(zone)}`,
    );
    if (!res.ok) throw new Error(await res.text());
    const rooms: SlimRoom[] = await res.json();
    this.zoneCache.set(zone, rooms);
    return rooms;
  }

  private async showZone(
    zone: string,
    opts: { resetView: boolean },
  ): Promise<void> {
    try {
      const rooms = await this.fetchZone(zone);
      this.currentZone = zone;
      if (this.zoneSelect.value !== zone) this.zoneSelect.value = zone;
      if (opts.resetView) {
        this.renderer.setZone(zone, rooms);
      } else {
        // Keep the caller's view; setZone recomputes focus, so only refresh data
        // via setZone when the zone actually changed identity.
        this.renderer.setZone(zone, rooms);
      }
      this.buildFloorStack();
      // Re-place the marker if the player is in this zone.
      if (this.lastPosition?.valid && this.lastPosition.zone === zone) {
        this.placeMarker(this.lastPosition);
      } else {
        this.renderer.setMarker(null);
      }
    } catch (err) {
      this.setStatus(
        `Failed to load zone: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  private buildFloorStack(): void {
    this.floorStack.innerHTML = "";
    const levels = this.renderer.levelList();
    if (levels.length <= 1) {
      this.floorStack.style.display = "none";
      return;
    }
    this.floorStack.style.display = "";
    for (const L of [...levels].sort((a, b) => b - a)) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = String(L);
      btn.className = L === this.renderer.level ? "on" : "";
      btn.addEventListener("click", () => {
        this.renderer.setLevel(L);
        [...this.floorStack.children].forEach((b) =>
          (b as HTMLElement).classList.toggle(
            "on",
            (b as HTMLElement).textContent === String(L),
          ),
        );
      });
      this.floorStack.appendChild(btn);
    }
  }

  // --- live position following ------------------------------------------

  // Handle a room_position WS broadcast. Updates the confidence badge and, when
  // the position is valid, the marker + (if following) the view.
  async onRoomPosition(pos: PlayerPosition): Promise<void> {
    this.lastPosition = pos;
    this.updateConfidence(pos);
    if (!pos.valid) {
      // Lost/red: keep the last marker where it was, just recolor via confidence.
      return;
    }
    await this.applyPosition(pos, this.followToggle.checked);
  }

  private updateConfidence(pos: PlayerPosition): void {
    const glyph = CONF_GLYPH[pos.confidence] ?? "⚪";
    const pending = pos.pendingMoves > 0 ? ` +${pos.pendingMoves}` : "";
    this.confidenceBadge.textContent = glyph + pending;
    let title = `Confidence: ${pos.confidence}`;
    if (pos.pendingMoves > 0)
      title += ` · ${pos.pendingMoves} unconfirmed move(s)`;
    if (pos.reason) title += ` · ${pos.reason}`;
    if (pos.isDT) title += " · ⚠ death trap";
    if (pos.pipe) title += " · corridor";
    this.confidenceBadge.title = title;
  }

  private async applyPosition(
    pos: PlayerPosition,
    follow: boolean,
  ): Promise<void> {
    if (!pos.valid) return;
    // Switch zone if the player left the currently displayed one and we're
    // following (or no zone is shown yet).
    if (
      pos.zone &&
      pos.zone !== this.currentZone &&
      (follow || !this.currentZone)
    ) {
      if (this.zoneNames.includes(pos.zone)) {
        await this.showZone(pos.zone, { resetView: false });
      }
    }
    if (pos.zone === this.currentZone) {
      this.placeMarker(pos);
      if (follow) this.renderer.centerOn(pos.x, pos.y, pos.l);
    }
  }

  private placeMarker(pos: PlayerPosition): void {
    const color = CONF_COLOR[pos.confidence] ?? CONF_COLOR.red;
    this.renderer.setMarker({ x: pos.x, y: pos.y, l: pos.l, color });
    // Reflect the floor in the stack highlight if the marker floor is active.
    [...this.floorStack.children].forEach((b) =>
      (b as HTMLElement).classList.toggle(
        "on",
        (b as HTMLElement).textContent === String(this.renderer.level),
      ),
    );
  }

  // --- seam jumps --------------------------------------------------------

  private async jumpSeam(
    seam: { zone: string; command: string; tag: number },
    _room: SlimRoom,
  ): Promise<void> {
    const target = findZoneName(seam.zone, this.zoneNames);
    if (!target) {
      this.setStatus(`Seam target zone "${seam.zone}" not found`);
      return;
    }
    await this.showZone(target, { resetView: true });
    const rooms = await this.fetchZone(target);
    const hit = resolveSeamTarget(seam, rooms);
    if (hit) {
      this.renderer.centerOn(hit.x, hit.y, hit.l);
    }
    this.setStatus(`Jumped via seam to "${target}"`);
  }

  // --- click a room → route into the command input -----------------------

  // Compute a route from the live tracked position to the clicked room and drop
  // the ';'-joined canonical directions (w;e;s;n;u;d) into the main command input
  // WITHOUT sending, so the user can review/edit/send. Soft edge cases: clicking
  // the current room is a no-op; a lost position shows a hint instead of inserting
  // garbage; a DT target still inserts the walk but surfaces the warning.
  private async pathToInput(room: SlimRoom): Promise<void> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot compute a route");
      return;
    }
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-path`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            to: { zone: room.z, x: room.x, y: room.y, l: room.l },
          }),
        },
      );
      if (!res.ok) throw new Error(await res.text());
      const data: MapPathResponse = await res.json();
      if (!data.reachable) {
        // Lost position / DT target / unreachable: hint, don't insert garbage.
        const reason = data.dt
          ? "target is a death trap"
          : data.reason || "no route";
        this.setStatus(`No route to "${room.h}" — ${reason}`);
        return;
      }
      const dirs = data.directions ?? [];
      if (data.here || dirs.length === 0) {
        this.setStatus(`Already at "${room.h}"`);
        return;
      }
      const walk = dirs.join(";");
      if (this.config.setInputText) {
        this.config.setInputText(walk);
        let status = `Route to "${room.h}": ${data.summary ?? `${dirs.length} step(s)`} → input`;
        if (data.dt) status += " ⚠ death trap on path";
        this.setStatus(status);
      } else {
        // No input hook wired — surface the walk so it is still usable.
        this.setStatus(`Route to "${room.h}": ${walk}`);
      }
    } catch (err) {
      this.setStatus(
        `Route failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // --- search ------------------------------------------------------------

  private async onSearch(): Promise<void> {
    const q = this.searchInput.value.trim().toLowerCase();
    if (!q) return;
    // Prefer the current zone; the map buffer holds one zone at a time, so search
    // the loaded zone first, then fall through with a hint to switch zones.
    const rooms = this.zoneCache.get(this.currentZone) || [];
    const hit = rooms.find((r) => r.h.toLowerCase().includes(q));
    if (hit) {
      this.renderer.setLevel(hit.l);
      this.buildFloorStack();
      this.renderer.centerOn(hit.x, hit.y, hit.l);
      this.setStatus(`Found "${hit.h}"`);
    } else {
      this.setStatus(`No match in "${this.currentZone}" — try another zone`);
    }
  }

  // --- "I'm here" re-anchor ---------------------------------------------

  private anchorMode = false;
  private toggleAnchorMode(): void {
    this.anchorMode = !this.anchorMode;
    this.anchorBtn.classList.toggle("active", this.anchorMode);
    if (this.anchorMode) {
      this.setStatus("Click a room to anchor the tracker there…");
      const handler = (e: MouseEvent) => {
        const rect = this.renderer.canvas.getBoundingClientRect();
        const px = e.clientX - rect.left;
        const py = e.clientY - rect.top;
        const room = this.renderer.pick(px, py);
        this.anchorMode = false;
        this.anchorBtn.classList.remove("active");
        this.renderer.canvas.removeEventListener("click", handler, true);
        if (room) void this.anchorHere(room);
        else this.setStatus("No room under cursor — anchor cancelled");
      };
      // Capture so it fires before the renderer's own click (which would seam-jump).
      this.renderer.canvas.addEventListener("click", handler, true);
    }
  }

  private async anchorHere(room: SlimRoom): Promise<void> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot anchor");
      return;
    }
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-anchor`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            zone: room.z,
            x: room.x,
            y: room.y,
            l: room.l,
          }),
        },
      );
      if (!res.ok) throw new Error(await res.text());
      this.setStatus(`Anchored at "${room.h}" — awaiting confirmation`);
    } catch (err) {
      this.setStatus(
        `Anchor failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // --- tooltip -----------------------------------------------------------

  private showTip(
    room: SlimRoom | null,
    clientX: number,
    clientY: number,
  ): void {
    if (!room) {
      this.tip.style.opacity = "0";
      return;
    }
    const rect = this.root.getBoundingClientRect();
    this.tip.style.left = `${clientX - rect.left}px`;
    this.tip.style.top = `${clientY - rect.top}px`;
    this.tip.style.opacity = "1";
    const dirs = room.e.join(" ") || "—";
    const name = room.p ? room.h || "corridor" : room.h;
    const parts = [`<div class="map-tip__name">${esc(name)}</div>`];
    parts.push(
      `<div class="map-tip__row">tag ${room.t ?? "—"} · L${room.l} · exits ${esc(dirs)}</div>`,
    );
    if (room.s)
      parts.push(`<div class="map-tip__row map-tip__dt">⚠ death trap</div>`);
    if (room.a.length > 0)
      parts.push(
        `<div class="map-tip__row map-tip__seam">◈ seam (click to cross)</div>`,
      );
    this.tip.innerHTML = parts.join("");
  }

  private setStatus(text: string): void {
    this.statusLine.textContent = text;
    this.statusLine.style.display = text ? "" : "none";
  }
}

function esc(s: string): string {
  return String(s).replace(
    /[&<>"]/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c] as string,
  );
}
