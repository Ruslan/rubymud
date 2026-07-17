import { fetchWithToken } from "../dom";
import {
  canRoute,
  cellPatchPayload,
  formatExitDiff,
  hasExitDiff,
  isCurrentCell,
  statusHeaderText,
  walkUpToFirstDoor,
} from "./cellMenu";
import { findZoneName, parseZoneList, resolveSeamTarget } from "./geometry";
import { MapRenderer } from "./renderer";
import type { Confidence, PlayerPosition, SlimRoom } from "./types";

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
  // Populate (replace + focus, do NOT send) the main command input — the cell
  // menu's "Вставить путь в чат" action. Optional so the map module stays
  // decoupled from the input DOM.
  setInputText?: (text: string) => void;
  // Actually submit a command to the MUD (reuses the Enter-submit path) — the
  // cell menu's "Отправить на сервер" action. Optional.
  sendCommand?: (text: string) => void;
}

// Endpoint response for POST /api/sessions/{id}/map-path.
interface MapPathResponse {
  reachable: boolean;
  directions?: string[];
  summary?: string;
  reason?: string;
  dt?: boolean;
  seams?: number;
  doors?: number;
  door_at?: number;
  here?: boolean;
}

// MapPaneController owns one map pane: its canvas renderer plus the surrounding
// controls (zone selector, floor stack, follow toggle, confidence badge). It
// fetches slim rooms lazily per zone from the REST feed and follows live
// `room_position` broadcasts. Clicking a room cell opens a context menu (route
// here / anchor here / update cell from live state); the top bar no longer
// carries an "I'm here" button — that action moved into the menu.
export class MapPaneController {
  readonly root: HTMLElement;
  private renderer: MapRenderer;
  private zoneSelect: HTMLSelectElement;
  private floorStack: HTMLElement;
  private confidenceBadge: HTMLElement;
  private searchInput: HTMLInputElement;
  private tip: HTMLElement;
  private followToggle: HTMLInputElement;
  private statusLine: HTMLElement;
  // The open cell context menu element (null when closed). Only one at a time.
  private cellMenu: HTMLElement | null = null;

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
    // Plain (non-seam) cell click opens the context menu; a seam cell keeps its
    // cross-zone jump on the amber-ring glyph (handled by onSeamClick above).
    this.renderer.onRoomClick = (room, clientX, clientY) =>
      this.openCellMenu(room, clientX, clientY);
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
    this.closeCellMenu();
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

  // --- cell context menu -------------------------------------------------

  // Open the per-cell context menu at the click position. The menu is built from
  // the live tracker position (confidence header + yellow exit-diff) plus the
  // clicked cell (route / anchor). Only one menu is open at a time.
  private openCellMenu(room: SlimRoom, clientX: number, clientY: number): void {
    this.closeCellMenu();
    const pos = this.lastPosition;
    const cell = { z: room.z, x: room.x, y: room.y, l: room.l };

    const menu = document.createElement("div");
    menu.className = "map-cell-menu";
    menu.setAttribute("role", "menu");

    // 1) Status header: confidence + reason, plus a yellow exit-diff + update.
    const header = document.createElement("div");
    header.className = "map-cell-menu__header";
    header.textContent = statusHeaderText(pos);
    menu.appendChild(header);

    if (hasExitDiff(pos)) {
      const diff = document.createElement("div");
      diff.className = "map-cell-menu__diff";
      diff.textContent = formatExitDiff(pos);
      menu.appendChild(diff);
      const updateBtn = this.menuButton(
        "Обновить клетку из живого стейта",
        () => void this.patchCellFromLive(pos!),
      );
      updateBtn.classList.add("map-cell-menu__update");
      menu.appendChild(updateBtn);
    }

    // 2) "Идти сюда" — insert into chat OR send to server.
    const goHeader = document.createElement("div");
    goHeader.className = "map-cell-menu__section";
    goHeader.textContent = `Идти сюда: ${room.h || "клетка"}`;
    menu.appendChild(goHeader);

    const routable = canRoute(pos) && !isCurrentCell(pos, cell);
    const insertBtn = this.menuButton(
      "Вставить путь в чат",
      () => void this.routeToCell(cell, room, "insert"),
    );
    const sendBtn = this.menuButton(
      "Отправить на сервер",
      () => void this.routeToCell(cell, room, "send"),
    );
    if (!routable) {
      insertBtn.disabled = true;
      sendBtn.disabled = true;
      const why = isCurrentCell(pos, cell)
        ? "вы уже здесь"
        : "нет позиции — сначала «Я здесь»";
      insertBtn.title = why;
      sendBtn.title = why;
    }
    menu.appendChild(insertBtn);
    menu.appendChild(sendBtn);

    // 3) "Я здесь" — anchor the tracker to this cell.
    const anchorBtn = this.menuButton(
      "Я здесь",
      () => void this.anchorHere(room),
    );
    anchorBtn.classList.add("map-cell-menu__anchor");
    menu.appendChild(anchorBtn);

    // Position at the click, relative to the pane root.
    const rect = this.root.getBoundingClientRect();
    menu.style.left = `${clientX - rect.left}px`;
    menu.style.top = `${clientY - rect.top}px`;
    this.root.appendChild(menu);
    this.cellMenu = menu;

    // Dismiss on outside-click / Esc. Defer the outside-click listener a tick so
    // the click that opened the menu does not immediately close it.
    setTimeout(() => {
      if (this.disposed || this.cellMenu !== menu) return;
      document.addEventListener("mousedown", this.onOutsideMenuClick, true);
      document.addEventListener("keydown", this.onMenuKeydown, true);
    }, 0);
  }

  private onOutsideMenuClick = (e: MouseEvent): void => {
    if (this.cellMenu && !this.cellMenu.contains(e.target as Node)) {
      this.closeCellMenu();
    }
  };

  private onMenuKeydown = (e: KeyboardEvent): void => {
    if (e.key === "Escape") this.closeCellMenu();
  };

  private closeCellMenu(): void {
    document.removeEventListener("mousedown", this.onOutsideMenuClick, true);
    document.removeEventListener("keydown", this.onMenuKeydown, true);
    if (this.cellMenu) {
      this.cellMenu.remove();
      this.cellMenu = null;
    }
  }

  private menuButton(label: string, onClick: () => void): HTMLButtonElement {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "map-cell-menu__item";
    btn.textContent = label;
    btn.addEventListener("click", () => {
      this.closeCellMenu();
      onClick();
    });
    return btn;
  }

  // Compute the route from the live position to `cell` and either insert it into
  // the command input or send it to the MUD. Soft edge cases handled inline.
  private async routeToCell(
    cell: { z: string; x: number; y: number; l: number },
    room: SlimRoom,
    action: "insert" | "send",
  ): Promise<void> {
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
            to: { zone: cell.z, x: cell.x, y: cell.y, l: cell.l },
          }),
        },
      );
      if (!res.ok) throw new Error(await res.text());
      const data: MapPathResponse = await res.json();
      if (!data.reachable) {
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
      if (action === "insert") {
        const walk = dirs.join(";");
        if (this.config.setInputText) {
          this.config.setInputText(walk);
          let status = `Route to "${room.h}": ${data.summary ?? `${dirs.length} step(s)`} → input`;
          if (data.dt) status += " ⚠ death trap on path";
          this.setStatus(status);
        } else {
          this.setStatus(`Route to "${room.h}": ${walk}`);
        }
        return;
      }
      // action === "send": avoid blindly batching through a (presumed-)door — send
      // only up to the first door hop, and hint the user to open + continue.
      const { walk, truncatedAtDoor } = walkUpToFirstDoor(
        dirs,
        data.door_at ?? -1,
      );
      if (!this.config.sendCommand) {
        this.setStatus(`Route to "${room.h}": ${dirs.join(";")}`);
        return;
      }
      if (walk.length === 0 && truncatedAtDoor) {
        // The very first hop is a door: don't auto-send; tell the user.
        this.setStatus(
          `Первый шаг к "${room.h}" — дверь: откройте её вручную, затем повторите`,
        );
        return;
      }
      this.config.sendCommand(walk.join(";"));
      let status = `Отправлено к "${room.h}": ${walk.length} шаг(ов)`;
      if (truncatedAtDoor) status += " · дальше дверь — откройте и повторите";
      if (data.dt) status += " ⚠ death trap on path";
      this.setStatus(status);
    } catch (err) {
      this.setStatus(
        `Route failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // POST the yellow exit-diff to the map-cell-patch endpoint so the live-observed
  // exits are written into the tracker's current map cell. On success the server
  // reloads the tracker index and broadcasts rooms_changed, which triggers our
  // reload() — so the edge becomes known (next visit → green).
  private async patchCellFromLive(pos: PlayerPosition): Promise<void> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot update the cell");
      return;
    }
    const { add_exits, remove_exits } = cellPatchPayload(pos);
    if (add_exits.length === 0 && remove_exits.length === 0) {
      this.setStatus("Нет изменений для применения");
      return;
    }
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-cell-patch`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            zone: pos.zone,
            x: pos.x,
            y: pos.y,
            l: pos.l,
            add_exits,
            remove_exits,
          }),
        },
      );
      if (!res.ok) throw new Error(await res.text());
      const data: { ok?: boolean; reason?: string } = await res.json();
      if (!data.ok) {
        this.setStatus(`Не удалось обновить клетку: ${data.reason ?? "?"}`);
        return;
      }
      this.setStatus("Клетка обновлена из живого стейта");
    } catch (err) {
      this.setStatus(
        `Update failed: ${err instanceof Error ? err.message : String(err)}`,
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

  // --- "Я здесь" re-anchor (invoked from the cell menu) ------------------

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
