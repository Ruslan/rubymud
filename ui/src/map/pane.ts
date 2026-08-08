import { fetchWithToken } from "../dom";
import {
  canRoute,
  cellPatchPayload,
  clampMenuPosition,
  formatExitDiff,
  ghostCandidate,
  hasExitDiff,
  isCurrentCell,
  nextStatusState,
  recordAction,
  recordButtonState,
  recordPreview,
  statusAutoHideDelay,
  statusHeaderText,
} from "./cellMenu";
import {
  attrsSummary,
  buildEdgeModel,
  canClear,
  dirName,
  markFor,
  provenanceLabel,
  renderDefaultOpen,
  seamAttrs,
  verticalMarks,
  type Cell,
  type EdgeMark,
  type EdgeModel,
  type EdgeSide,
  type EdgeAttrs,
} from "./edgeEditor";
import {
  findZoneName,
  parseZoneList,
  resolveSeamTarget,
  seamDirection,
  seamsOf,
} from "./geometry";
import { MapRenderer } from "./renderer";
import {
  canonicalZoneName,
  canonicalCoords,
  postZoneMove,
  previewSummary,
  type ZoneCoord,
  type ZoneMovePreview,
} from "./zoneCrud";
import type {
  Confidence,
  LiveRoom,
  MapRoomSearchResult,
  PlayerPosition,
  SlimRoom,
  SeamTarget,
} from "./types";

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
let nextZoneDialogID = 0;

// Reserved system buffer name for the map pane. The server rejects it as a
// trigger TargetBuffer; here it is the content-type trigger.
export const MAP_BUFFER = "~map";

type AttrPatch = {
  weight?: number;
  phrase?: string;
  door?: boolean;
  locked?: boolean;
  open_cmd?: string;
  no_route?: boolean;
};

type AttrControlBinding = {
  sync(attrs: EdgeAttrs): void;
};

type EdgeSideBlock = {
  element: HTMLElement;
  attrs: AttrControlBinding | null;
};

function applyAttrPatch(attrs: EdgeAttrs, patch: AttrPatch): EdgeAttrs {
  return {
    ...attrs,
    ...(patch.weight !== undefined ? { weight: patch.weight } : {}),
    ...(patch.phrase !== undefined ? { phrase: patch.phrase } : {}),
    ...(patch.door !== undefined ? { door: patch.door } : {}),
    ...(patch.locked !== undefined ? { locked: patch.locked } : {}),
    ...(patch.open_cmd !== undefined ? { openCmd: patch.open_cmd } : {}),
    ...(patch.no_route !== undefined ? { noRoute: patch.no_route } : {}),
  };
}

// NO CONSTANT LIVES HERE. The command that opens an unannotated door is a property
// of the MUD on the other end, carried by the session's ROOM-FORMAT PROFILE
// (mapper.ProfileSpec.DoorOpen) and delivered in the `mapper_profile` message; a
// literal in the client would show "откр две север" to a player on an English Diku.
// The pane holds whatever the server told it, "" until it is told anything — and ""
// is also the honest value for a format that describes no door verb, in which case
// the editor says so instead of promising a command.

// describeAttrPatch renders what a one-field attribute write just did, for the
// status line. It reads the patch rather than the new state so the message names
// the thing the operator actually touched.
function describeAttrPatch(
  patch: AttrPatch,
  dir: string,
  doorOpen: string,
): string {
  if (patch.phrase !== undefined) {
    return patch.phrase
      ? `passage uses '${patch.phrase}' instead of the direction`
      : "traversal phrase removed — the edge uses its direction again";
  }
  if (patch.door !== undefined) {
    return patch.door
      ? "door marked — the route sends an opening command before the step"
      : "door mark removed";
  }
  if (patch.locked !== undefined) {
    return patch.locked
      ? "door marked locked by default from this side"
      : "locked mark removed";
  }
  if (patch.open_cmd !== undefined) {
    if (patch.open_cmd) return `opening command: '${patch.open_cmd}'`;
    return doorOpen
      ? `opening command reset — the route sends '${renderDefaultOpen(doorOpen, dir)}'`
      : "opening command reset — this MUD's room format does not open doors," +
        " so the route sends nothing";
  }
  if (patch.no_route !== undefined) {
    return patch.no_route
      ? "routes will avoid this edge (manual movement still works)"
      : "routing through this edge is allowed again";
  }
  return "edge attribute saved";
}

interface MapPaneConfig {
  sessionID: number | undefined;
  getActiveMapSetID: () => number | null;
  // Populate (replace + focus, do NOT send) the main command input — the cell
  // menu's "Вставить путь в чат" action. Optional so the map module stays
  // decoupled from the input DOM.
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
  doors?: number;
  total_weight?: number;
  // How many hops cross a door the operator marked LOCKED by default. The route
  // still plans through them (the player may hold the key), so this is a warning.
  locked?: number;
  phrases?: number;
  // The target is unreachable ONLY because of hand-drawn "не водить сюда" marks.
  vetoed_only?: boolean;
  here?: boolean;
}

export interface MapWalkState {
  active: boolean;
  epoch?: number;
  job_id?: number;
  current: number;
  total: number;
  reason?: string;
  state: string;
  can_resume?: boolean;
  revision?: number;
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
  private recordButton: HTMLButtonElement;
  private walkProgress: HTMLElement;
  private walkResumeButton: HTMLButtonElement;
  private walkStopButton: HTMLButtonElement;
  private searchInput: HTMLInputElement;
  private searchButton: HTMLButtonElement;
  private managerButton: HTMLButtonElement;
  private searchPopover: HTMLElement | null = null;
  private managerPopover: HTMLElement | null = null;
  private searchOpener: HTMLElement | null = null;
  private managerOpener: HTMLElement | null = null;
  private searchResults: HTMLElement | null = null;
  private searchTimer: ReturnType<typeof window.setTimeout> | null = null;
  private searchRequest: AbortController | null = null;
  private searchGeneration = 0;
  private popoverListeners = new AbortController();
  private tip: HTMLElement;
  private followToggle: HTMLInputElement;
  private statusLine: HTMLElement;
  private statusText: HTMLElement;
  private statusTimer: ReturnType<typeof window.setTimeout> | null = null;
  // The open cell context menu element (null when closed). Only one at a time.
  private cellMenu: HTMLElement | null = null;
  private cellMenuAnchor: { clientX: number; clientY: number } | null = null;
  // Attribute menus bind to a room/edge in the cached map. A refresh may replace
  // that map underneath them, so close the popup rather than leave an editor for
  // a disappeared transition on screen.
  private cellMenuValid: (() => boolean) | null = null;
  private portalWriteGeneration = 0;

  private mapSetID: number | null = null;
  private zoneNames: string[] = [];
  private zoneCache = new Map<string, SlimRoom[]>();
  private currentZone = "";
  private lastPosition: PlayerPosition | null = null;
  // The live room block (the `room` WS message) — what «Записать комнату» writes.
  private liveRoom: LiveRoom | null = null;
  // When a sticky status message was set (0 = none). See setStatus.
  private stickyStatusAt = 0;
  // The session's room-format door-opening template, from the server (see above).
  private doorOpen = "";
  private walk: MapWalkState = {
    active: false,
    current: 0,
    total: 0,
    state: "stopped",
  };
  // A REST start response can arrive after the WebSocket completion for the
  // same job. Keep its terminal generation so that older active snapshots never
  // resurrect a finished route.
  private walkTerminalJobID = 0;
  private walkSeenJobID = 0;
  private walkEpoch = 0;
  private disposed = false;
  private loadGeneration = 0;
  private attrWriteGeneration = new Map<string, number>();
  private attrWriteTail = new Map<string, Promise<void>>();
  private selectionToggle: HTMLButtonElement;
  private selectionCount: HTMLElement;
  private moveZoneButton: HTMLButtonElement;
  private renameZoneButton: HTMLButtonElement;
  private selectedCoords: ZoneCoord[] = [];
  private zoneDialog: HTMLElement | null = null;
  private zoneDialogOpener: HTMLElement | null = null;
  private zoneRequest: AbortController | null = null;
  private zoneRequestGeneration = 0;
  private zoneListRequest: AbortController | null = null;
  private zoneListGeneration = 0;
  private zoneOperationBusy = false;
  private keyboardListeners = new AbortController();

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
      void this.showZone(this.zoneSelect.value, { resetView: true }, this.nextLoadGeneration());
    });
    bar.appendChild(this.zoneSelect);

    this.searchButton = this.iconButton("🔍", "Find room");
    this.searchButton.addEventListener("click", () => this.toggleSearchPopover());
    bar.appendChild(this.searchButton);

    this.searchInput = document.createElement("input");
    this.searchInput.type = "search";
    this.searchInput.className = "map-pane__search";
    this.searchInput.placeholder = "Find room";
    this.searchInput.addEventListener("input", () => void this.onSearch());

    this.managerButton = this.iconButton("⌗", "Zone manager");
    this.managerButton.addEventListener("click", () => this.toggleManagerPopover());
    bar.appendChild(this.managerButton);

    this.selectionToggle = document.createElement("button");
    this.selectionToggle.type = "button";
    this.selectionToggle.className = "map-pane__tool";
    this.selectionToggle.textContent = "⌗";
    this.selectionToggle.title = "Select rooms";
    this.selectionToggle.setAttribute("aria-label", "Select rooms");
    this.selectionToggle.setAttribute("aria-pressed", "false");
    this.selectionToggle.addEventListener("click", () => this.toggleSelectionMode());
    this.selectionToggle.hidden = true;
    bar.appendChild(this.selectionToggle);

    this.selectionCount = document.createElement("span");
    this.selectionCount.className = "map-pane__selection-count";
    this.selectionCount.setAttribute("aria-live", "polite");
    this.selectionCount.hidden = true;
    this.managerButton.appendChild(this.selectionCount);

    this.moveZoneButton = document.createElement("button");
    this.moveZoneButton.type = "button";
    this.moveZoneButton.className = "map-pane__zone-command";
    this.moveZoneButton.textContent = "Move to zone…";
    this.moveZoneButton.title = "Move selected rooms to a zone";
    this.moveZoneButton.disabled = true;
    this.moveZoneButton.addEventListener("click", () => this.openZoneDialog("move"));
    this.moveZoneButton.hidden = true;
    bar.appendChild(this.moveZoneButton);

    this.renameZoneButton = document.createElement("button");
    this.renameZoneButton.type = "button";
    this.renameZoneButton.className = "map-pane__zone-command";
    this.renameZoneButton.textContent = "Rename zone…";
    this.renameZoneButton.title = "Rename the current zone";
    this.renameZoneButton.disabled = true;
    this.renameZoneButton.addEventListener("click", () => this.openZoneDialog("rename"));
    this.renameZoneButton.hidden = true;
    bar.appendChild(this.renameZoneButton);

    const followLabel = document.createElement("label");
    followLabel.className = "map-pane__follow";
    followLabel.title = "Follow the live player position";
    this.followToggle = document.createElement("input");
    this.followToggle.type = "checkbox";
    this.followToggle.setAttribute("aria-label", "Follow the live player position");
    this.followToggle.checked = true;
    followLabel.classList.add("on");
    this.followToggle.addEventListener("change", () => {
      followLabel.classList.toggle("on", this.followToggle.checked);
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

    // «Записать комнату» next to the confidence badge — the second entry point to
    // the SAME write the cell menu offers, for the case the menu is bad at: a new
    // location, where every room is a fresh record and hunting for the right cell
    // to right-click every time is the whole friction. It never grows its own
    // write logic; it re-uses recordRoom() with the tracker's own coord.
    this.recordButton = document.createElement("button");
    this.recordButton.type = "button";
    this.recordButton.className = "map-pane__record";
    this.recordButton.addEventListener("click", () => void this.onRecordClick());
    bar.appendChild(this.recordButton);
    this.updateRecordButton();

    this.walkProgress = document.createElement("span");
    this.walkProgress.className = "map-pane__walk-progress";
    this.walkProgress.hidden = true;
    bar.appendChild(this.walkProgress);

    this.walkResumeButton = document.createElement("button");
    this.walkResumeButton.type = "button";
    this.walkResumeButton.className = "map-pane__walk-resume";
    this.walkResumeButton.textContent = "Continue";
    this.walkResumeButton.setAttribute("aria-label", "Continue paused map walk");
    this.walkResumeButton.title = "Continue paused map walk";
    this.walkResumeButton.hidden = true;
    this.walkResumeButton.addEventListener("click", () => void this.resumeMapWalk());
    bar.appendChild(this.walkResumeButton);

    this.walkStopButton = document.createElement("button");
    this.walkStopButton.type = "button";
    this.walkStopButton.className = "map-pane__walk-stop";
    this.walkStopButton.textContent = "Stop";
    this.walkStopButton.setAttribute("aria-label", "Stop map walk");
    this.walkStopButton.title = "Stop map walk";
    this.walkStopButton.hidden = true;
    this.walkStopButton.addEventListener("click", () => void this.cancelMapWalk());
    bar.appendChild(this.walkStopButton);

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
    this.statusText = document.createElement("span");
    this.statusText.className = "map-pane__status-text";
    this.statusLine.appendChild(this.statusText);
    const closeStatus = document.createElement("button");
    closeStatus.type = "button";
    closeStatus.className = "map-pane__status-close";
    closeStatus.textContent = "×";
    closeStatus.setAttribute("aria-label", "Dismiss message");
    closeStatus.title = "Dismiss message";
    closeStatus.addEventListener("click", () => this.dismissStatus());
    this.statusLine.appendChild(closeStatus);
    this.statusLine.style.display = "none";
    stage.appendChild(this.statusLine);

    this.root.appendChild(stage);

    this.renderer = new MapRenderer(canvas);
    // Every room, including one with seam metadata, opens the same cell menu.
    // Cross-zone jumps are direct actions in the room menu.
    this.renderer.onRoomClick = (room, clientX, clientY) =>
      this.openCellMenu(
        { z: room.z, x: room.x, y: room.y, l: room.l },
        room,
        clientX,
        clientY,
      );
    // The candidate (ghost) cell has no SlimRoom behind it — the menu opens with
    // room=null and offers the "draw a new room here" record action.
    this.renderer.onGhostClick = (ghost, clientX, clientY) =>
      this.openCellMenu(
        { z: this.currentZone, x: ghost.x, y: ghost.y, l: ghost.l },
        null,
        clientX,
        clientY,
      );
    // Click on the boundary BETWEEN two cells opens the edge editor — a separate
    // surface from the cell menu, because an edge is two directed facts and a cell
    // menu can only speak about one cell.
    this.renderer.onEdgeClick = (room, dir, clientX, clientY) =>
      this.openEdgeMenu(
        { z: this.currentZone, x: room.x, y: room.y, l: room.l },
        dir,
        clientX,
        clientY,
      );
    this.renderer.onRoomHover = (room, cx, cy) => this.showTip(room, cx, cy);
    this.renderer.onSelectionChange = (coords) => this.setSelection(coords);
    this.renderer.onLevelChange = () => {
      this.closeZoneDialog();
      this.closeSearchPopover(false);
      this.closeManagerPopover(false);
      this.clearZoneSelection();
    };
    window.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && this.searchPopover) {
        event.preventDefault(); this.closeSearchPopover(); return;
      }
      if (event.key === "Escape" && this.managerPopover) {
        event.preventDefault(); this.closeManagerPopover(); return;
      }
      if (event.key !== "Escape" || this.zoneDialog || !this.renderer.isSelectionMode()) return;
      event.preventDefault();
      if (this.selectedCoords.length > 0) this.clearZoneSelection();
      else this.exitSelectionMode();
    }, { signal: this.keyboardListeners.signal });

    void this.loadMapSet({}, this.nextLoadGeneration());
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
    this.loadGeneration++;
    this.clearStatusTimer();
    this.closeCellMenu();
    this.closeSearchPopover(false);
    this.closeManagerPopover(false);
    this.closeZoneDialog(false);
    this.keyboardListeners.abort();
    this.renderer.destroy();
  }

  // Force a canvas repaint (e.g. on theme refresh).
  render(): void {
    this.renderer.render();
  }

  // Reload the active corpus. A COW topology write changes its id but not the
  // administrator's intent to inspect a remote zone, unlike an explicit active
  // map-set selection which keeps the historical reset-to-player behaviour.
  async reload(opts: { preserveView?: boolean; applyPosition?: boolean } = {}): Promise<void> {
    if (opts.preserveView !== true) this.closeZoneDialog();
    const generation = this.nextLoadGeneration();
    const preserveView = opts.preserveView === true;
    const preferredZone = preserveView ? this.currentZone : undefined;
    this.zoneCache.clear();
    await this.loadMapSet({
      preferredZone,
      preserveView: preferredZone !== undefined,
      applyPosition: opts.applyPosition ?? !preserveView,
    }, generation);
  }

  // Refresh rooms within the active corpus after an editor write. This keeps an
  // administrator's selected zone and camera independent from player follow.
  async refreshRooms(): Promise<void> {
    await this.reload({ preserveView: true, applyPosition: false });
  }

  private nextLoadGeneration(): number { return ++this.loadGeneration; }

  private isCurrentLoad(generation: number, mapSetID: number | null): boolean {
    return !this.disposed && generation === this.loadGeneration && mapSetID === this.mapSetID;
  }

  private clearMapView(): void {
    this.clearZoneSelection();
    this.zoneCache.clear();
    this.zoneNames = [];
    this.currentZone = "";
    this.renameZoneButton.disabled = true;
    this.zoneSelect.innerHTML = "";
    this.floorStack.innerHTML = "";
    this.floorStack.style.display = "none";
    this.closeCellMenu();
    this.closeSearchPopover(false);
    this.closeManagerPopover(false);
    this.renderer.clear();
  }

  // Fetch the live room the SERVER currently holds. Without this a page reload
  // leaves the pane with no live room — and therefore nothing to preview — while
  // the backend would still happily record one, so the UI's gate and the
  // backend's state disagree. Best-effort: a failure just leaves liveRoom unset,
  // which disables the record action (the safe direction).
  private async fetchLiveRoom(generation: number, mapSetID: number | null): Promise<void> {
    if (this.config.sessionID == null) return;
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-live-room`,
      );
      if (!res.ok) return;
      const data: {
        seen?: boolean;
        hint?: string;
        desc?: string;
        exits?: string;
      } = await res.json();
      if (!this.isCurrentLoad(generation, mapSetID) || !data.seen) return;
      // A `room` broadcast that landed while this request was in flight is
      // fresher than the reply — do not overwrite it.
      if (this.liveRoom) return;
      this.liveRoom = {
        hint: data.hint ?? "",
        desc: data.desc ?? "",
        exits: data.exits ?? "",
      };
      this.refreshGhost();
    } catch {
      // ignore — no live room is the safe default
    }
  }

  private async loadMapSet(opts: {
    preferredZone?: string;
    preserveView?: boolean;
    applyPosition?: boolean;
    mapSetID?: number;
  } = {}, generation = this.nextLoadGeneration()): Promise<void> {
    const id = opts.mapSetID ?? this.config.getActiveMapSetID();
    if (id !== this.mapSetID) {
      this.closeZoneDialog();
      this.closeSearchPopover(false);
      this.closeManagerPopover(false);
    }
    const preserveSelection = opts.preserveView === true &&
      opts.preferredZone === this.currentZone && id === this.mapSetID;
    this.mapSetID = id;
    if (!preserveSelection) this.clearZoneSelection();
    void this.fetchLiveRoom(generation, id);
    if (id == null) {
      if (!this.isCurrentLoad(generation, id)) return;
      this.clearMapView();
      this.setStatus("No active map set. Select one in session settings.");
      return;
    }
    try {
      const res = await fetchWithToken(`/api/map-sets/${id}/zones`);
      if (!res.ok) throw new Error(await res.text());
      // /api/map-sets/{id}/zones returns [{zone, room_count}], NOT string[].
      const infos = parseZoneList(await res.json());
      if (!this.isCurrentLoad(generation, id)) return;
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
      if (this.zoneNames.length === 0) {
        this.clearMapView();
        return;
      }
      const preferred = opts.preferredZone;
      const start =
        preferred && this.zoneNames.includes(preferred)
          ? preferred
          : this.lastPosition?.valid &&
              this.zoneNames.includes(this.lastPosition.zone)
            ? this.lastPosition.zone
            : this.zoneNames[0];
      if (start) {
        this.zoneSelect.value = start;
        await this.showZone(start, {
          resetView: !(opts.preserveView && start === preferred),
        }, generation);
        // Re-apply a pending position now that a zone is loaded.
        if (this.isCurrentLoad(generation, id) && opts.applyPosition !== false && this.lastPosition?.valid)
          await this.applyPosition(
            this.lastPosition,
            this.followToggle.checked,
          );
      }
    } catch (err) {
      if (!this.isCurrentLoad(generation, id)) return;
      this.setStatus(
        `Failed to load zones: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  private async fetchZone(
    zone: string,
    generation: number,
    mapSetID: number | null,
    signal?: AbortSignal,
  ): Promise<SlimRoom[] | null> {
    if (this.zoneCache.has(zone)) return this.zoneCache.get(zone)!;
    if (mapSetID == null) return [];
    const res = await fetchWithToken(
      `/api/rooms?map_set=${mapSetID}&zone=${encodeURIComponent(zone)}`,
      signal ? { signal } : undefined,
    );
    if (!res.ok) throw new Error(await res.text());
    const rooms: SlimRoom[] = await res.json();
    if (!this.isCurrentLoad(generation, mapSetID)) return null;
    this.zoneCache.set(zone, rooms);
    return rooms;
  }

  private async showZone(
    zone: string,
    opts: { resetView: boolean },
    generation = this.nextLoadGeneration(),
  ): Promise<SlimRoom[] | null> {
    try {
      if (zone !== this.currentZone) {
        this.closeZoneDialog();
        this.closeSearchPopover(false);
        this.closeManagerPopover(false);
        this.clearZoneSelection();
      }
      const preserveTransform = !opts.resetView && zone === this.currentZone;
      const mapSetID = this.mapSetID;
      const rooms = await this.fetchZone(zone, generation, mapSetID);
      if (rooms === null || !this.isCurrentLoad(generation, mapSetID)) return null;
      this.currentZone = zone;
      this.renameZoneButton.disabled = this.zoneOperationBusy || this.config.sessionID == null;
      if (this.zoneSelect.value !== zone) this.zoneSelect.value = zone;
      if (preserveTransform) this.renderer.replaceRooms(rooms);
      else this.renderer.setZone(zone, rooms);
      this.buildFloorStack();
      // Re-place the marker if the player is in this zone.
      if (this.lastPosition?.valid && this.lastPosition.zone === zone) {
        this.placeMarker(this.lastPosition);
      } else {
        this.renderer.setMarker(null);
      }
      // The zone's rooms just changed, so whether the player's coord is mapped
      // may have changed too (this is also what turns a freshly RECORDED ghost
      // back into a real room after rooms_changed → reload()).
      this.refreshGhost();
      this.reconcileCellMenu();
      return rooms;
    } catch (err) {
      if (!this.isCurrentLoad(generation, this.mapSetID)) return null;
      this.setStatus(
        `Failed to load zone: ${err instanceof Error ? err.message : String(err)}`,
      );
      return null;
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
        this.clearZoneSelection();
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

  // --- zone operations ---------------------------------------------------

  private toggleSelectionMode(): void {
    if (this.renderer.isSelectionMode()) {
      this.exitSelectionMode();
      return;
    }
    this.closeCellMenu();
    this.renderer.setSelectionMode(true);
    this.selectionToggle.setAttribute("aria-pressed", "true");
    this.selectionToggle.classList.add("on");
    this.managerButton.classList.add("on");
    this.closeManagerPopover(false);
  }

  private exitSelectionMode(): void {
    this.renderer.setSelectionMode(false);
    this.clearZoneSelection();
    this.selectionToggle.setAttribute("aria-pressed", "false");
    this.selectionToggle.classList.remove("on");
    this.managerButton.classList.remove("on");
  }

  private setSelection(coords: ZoneCoord[]): void {
    this.selectedCoords = canonicalCoords(coords);
    const count = this.selectedCoords.length;
    this.selectionCount.hidden = count === 0;
    this.selectionCount.textContent = String(count);
    this.selectionCount.title = `${count} selected room${count === 1 ? "" : "s"}`;
    this.selectionCount.setAttribute("aria-label", this.selectionCount.title);
    this.moveZoneButton.disabled = this.zoneOperationBusy || count === 0 || count > 8000 || this.config.sessionID == null;
    this.managerButton.classList.toggle("on", this.renderer.isSelectionMode() || count > 0);
  }

  private clearZoneSelection(): void {
    this.selectedCoords = [];
    this.selectionCount.hidden = true;
    this.selectionCount.textContent = "";
    this.selectionCount.setAttribute("aria-label", "0 selected rooms");
    this.moveZoneButton.disabled = true;
    this.managerButton.classList.toggle("on", this.renderer?.isSelectionMode() ?? false);
    this.renderer?.clearSelection(false);
  }

  private iconButton(icon: string, label: string): HTMLButtonElement {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "map-pane__icon";
    button.textContent = icon;
    button.title = label;
    button.setAttribute("aria-label", label);
    return button;
  }

  private toggleSearchPopover(): void {
    if (this.searchPopover) { this.closeSearchPopover(); return; }
    this.closeManagerPopover(false);
    this.popoverListeners.abort();
    this.popoverListeners = new AbortController();
    this.searchOpener = this.searchButton;
    const popover = document.createElement("div");
    popover.className = "map-pane__popover map-pane__search-popover";
    popover.setAttribute("role", "dialog");
    popover.setAttribute("aria-label", "Find room");
    this.searchResults = document.createElement("div");
    this.searchResults.className = "map-pane__search-results";
    popover.append(this.searchInput, this.searchResults);
    this.root.appendChild(popover);
    this.searchPopover = popover;
    this.placePopover(popover, this.searchButton);
    queueMicrotask(() => {
      if (this.searchPopover === popover) this.searchInput.focus();
    });
    const close = (event: MouseEvent) => {
      if (!popover.contains(event.target as Node) && !this.searchButton.contains(event.target as Node)) this.closeSearchPopover();
    };
    document.addEventListener("mousedown", close, { signal: this.popoverListeners.signal });
    void this.onSearch();
  }

  private closeSearchPopover(returnFocus = true): void {
    if (!this.searchPopover) return;
    this.cancelMapSearch();
    this.searchPopover.remove();
    this.searchPopover = null;
    this.searchResults = null;
    this.popoverListeners.abort();
    if (returnFocus) this.searchOpener?.focus();
    this.searchOpener = null;
  }

  private toggleManagerPopover(): void {
    if (this.managerPopover) { this.closeManagerPopover(); return; }
    this.closeSearchPopover(false);
    this.popoverListeners.abort();
    this.popoverListeners = new AbortController();
    this.managerOpener = this.managerButton;
    const popover = document.createElement("div");
    popover.className = "map-pane__popover map-pane__manager-popover";
    popover.setAttribute("role", "dialog");
    popover.setAttribute("aria-label", "Zone manager");
    const select = document.createElement("button");
    select.type = "button";
    select.className = "map-pane__manager-action";
    select.textContent = this.renderer.isSelectionMode() ? "Stop selecting" : "Select rooms";
    select.addEventListener("click", () => {
      this.toggleSelectionMode();
      this.managerButton.focus();
    });
    const count = document.createElement("div");
    count.className = "map-pane__manager-count";
    count.textContent = `${this.selectedCoords.length} selected`;
    this.moveZoneButton.hidden = false;
    this.renameZoneButton.hidden = false;
    popover.append(select, count, this.moveZoneButton, this.renameZoneButton);
    this.root.appendChild(popover);
    this.managerPopover = popover;
    this.placePopover(popover, this.managerButton);
    queueMicrotask(() => {
      if (this.managerPopover === popover) select.focus();
    });
    const close = (event: MouseEvent) => {
      if (!popover.contains(event.target as Node) && !this.managerButton.contains(event.target as Node)) this.closeManagerPopover();
    };
    document.addEventListener("mousedown", close, { signal: this.popoverListeners.signal });
  }

  private closeManagerPopover(returnFocus = true): void {
    if (!this.managerPopover) return;
    this.moveZoneButton.hidden = true;
    this.renameZoneButton.hidden = true;
    this.root.querySelector(".map-pane__bar")?.append(this.moveZoneButton, this.renameZoneButton);
    this.managerPopover.remove();
    this.managerPopover = null;
    this.popoverListeners.abort();
    if (returnFocus) this.managerOpener?.focus();
    this.managerOpener = null;
  }

  private placePopover(popover: HTMLElement, opener: HTMLElement): void {
    const root = this.root.getBoundingClientRect();
    const anchor = opener.getBoundingClientRect();
    const width = Math.min(300, Math.max(180, root.width - 12));
    popover.style.width = `${width}px`;
    const left = Math.max(6, Math.min(anchor.left - root.left, root.width - width - 6));
    popover.style.left = `${left}px`;
    popover.style.top = `${Math.max(6, anchor.bottom - root.top + 4)}px`;
  }

  private openZoneDialog(kind: "move" | "rename"): void {
    if (this.zoneOperationBusy || this.zoneDialog || !this.currentZone || this.config.sessionID == null) return;
    const opener = this.managerPopover
      ? this.managerButton
      : document.activeElement instanceof HTMLElement ? document.activeElement : null;
    this.closeManagerPopover(false);
    const source = this.currentZone;
    const selection = canonicalCoords(this.selectedCoords);
    if (kind === "move" && (selection.length === 0 || selection.length > 8000)) {
      this.setStatus(selection.length > 8000 ? "Selection exceeds 8000 rooms" : "Select rooms first", { sticky: true });
      return;
    }

    this.closeCellMenu();
    this.zoneDialogOpener = opener;
    const backdrop = document.createElement("div");
    backdrop.className = "map-zone-dialog-backdrop";
    const dialog = document.createElement("dialog");
    dialog.className = "map-zone-dialog";
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.setAttribute("aria-label", kind === "move" ? "Move to zone" : "Rename zone");
    backdrop.appendChild(dialog);

    const title = document.createElement("h2");
    title.textContent = kind === "move" ? "Move to zone" : "Rename zone";
    dialog.appendChild(title);
    const sourceLine = document.createElement("div");
    sourceLine.className = "map-zone-dialog__source";
    sourceLine.textContent = source;
    dialog.appendChild(sourceLine);

    const existingLabel = document.createElement("label");
    existingLabel.textContent = "Existing zone";
    const search = document.createElement("input");
    search.type = "search";
    search.placeholder = "Search zones";
    search.className = "map-zone-dialog__search";
    existingLabel.appendChild(search);
    const existing = document.createElement("select");
    existing.className = "map-zone-dialog__select";
    existingLabel.appendChild(existing);
    dialog.appendChild(existingLabel);

    const newLabel = document.createElement("label");
    newLabel.textContent = "New zone";
    const newName = document.createElement("input");
    newName.type = "text";
    newName.autocomplete = "off";
    newLabel.appendChild(newName);
    dialog.appendChild(newLabel);

    const info = document.createElement("div");
    info.className = "map-zone-dialog__result";
    info.setAttribute("aria-live", "polite");
    info.setAttribute("aria-atomic", "true");
    info.setAttribute("role", "status");
    info.id = `map-zone-dialog-result-${++nextZoneDialogID}`;
    dialog.setAttribute("aria-describedby", info.id);
    dialog.appendChild(info);
    const actions = document.createElement("div");
    actions.className = "map-zone-dialog__actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.textContent = "Cancel";
    const preview = document.createElement("button");
    preview.type = "button";
    preview.textContent = "Preview";
    const apply = document.createElement("button");
    apply.type = "button";
    apply.textContent = "Apply";
    apply.disabled = true;
    actions.append(cancel, preview, apply);
    dialog.appendChild(actions);

    const renderOptions = () => {
      const needle = search.value.trim().toLocaleLowerCase();
      const prior = existing.value;
      existing.innerHTML = "";
      const blank = document.createElement("option");
      blank.value = "";
      blank.textContent = "Select a zone";
      existing.appendChild(blank);
      for (const zone of this.zoneNames) {
        if (zone === source || (needle && !zone.toLocaleLowerCase().includes(needle))) continue;
        const option = document.createElement("option");
        option.value = zone;
        option.textContent = zone;
        existing.appendChild(option);
      }
      if ([...existing.options].some((option) => option.value === prior)) existing.value = prior;
    };
    renderOptions();

    const listController = new AbortController();
    this.zoneListRequest?.abort();
    this.zoneListRequest = listController;
    const listGeneration = ++this.zoneListGeneration;
    const listMapSetID = this.mapSetID;
    void (async () => {
      if (listMapSetID == null) return;
      try {
        const response = await fetchWithToken(`/api/map-sets/${listMapSetID}/zones`, { signal: listController.signal });
        if (!response.ok) throw new Error(await response.text());
        const names = parseZoneList(await response.json()).map((zone) => zone.zone);
        if (listController.signal.aborted || this.zoneDialog !== backdrop ||
          listGeneration !== this.zoneListGeneration || this.mapSetID !== listMapSetID) return;
        this.zoneNames = names;
        renderOptions();
      } catch (err) {
        if (listController.signal.aborted || this.zoneDialog !== backdrop || listGeneration !== this.zoneListGeneration) return;
        if (!info.textContent) {
          info.textContent = `Failed to load zones: ${err instanceof Error ? err.message : String(err)}`;
          info.dataset.error = "true";
        }
      }
    })();

    let previewResult: ZoneMovePreview | null = null;
    let applying = false;
    const target = (): string => canonicalZoneName(newName.value || existing.value).value;
    const invalidate = () => {
      // A response for the old target must not re-enable Apply after an
      // operator has already changed the form.
      this.zoneRequest?.abort();
      this.zoneRequest = null;
      this.zoneRequestGeneration++;
      previewResult = null;
      preview.disabled = false;
      apply.disabled = true;
      if (info.dataset.error !== "true") info.textContent = "";
      info.dataset.error = "false";
    };
    const setError = (message: string) => {
      info.textContent = message;
      info.dataset.error = "true";
      apply.disabled = true;
    };
    const body = (withRevision = false): Record<string, unknown> => {
      const result: Record<string, unknown> = { source_zone: source, target_zone: target() };
      if (kind === "move") result.rooms = selection;
      if (withRevision && previewResult) result.expected_revision = previewResult.revision;
      return result;
    };
    const latest = (generation: number) => !this.disposed && this.zoneDialog === backdrop && generation === this.zoneRequestGeneration;
    const request = async (applyNow: boolean) => {
      const checkedTarget = canonicalZoneName(newName.value || existing.value);
      const targetZone = checkedTarget.value;
      if (checkedTarget.error) {
        setError(checkedTarget.error === "Zone name is required" ? "Choose an existing zone or enter a new name" : checkedTarget.error);
        return;
      }
      if (targetZone === source) { setError("Source and target zones must differ"); return; }
      if (applyNow && !previewResult) return;
      this.zoneRequest?.abort();
      const controller = new AbortController();
      this.zoneRequest = controller;
      const generation = ++this.zoneRequestGeneration;
      preview.disabled = true;
      apply.disabled = true;
      if (applyNow) applying = true;
      info.dataset.error = "false";
      info.textContent = applyNow ? "Applying…" : "Previewing…";
      const path = kind === "rename"
        ? (applyNow ? "map-zones/rename/apply" : "map-zones/rename/preview")
        : (applyNow ? "map-zones/apply" : "map-zones/preview");
      try {
        const response = await postZoneMove(this.config.sessionID!, path, body(applyNow), controller.signal);
        if (!latest(generation)) return;
        if (!response.ok || !response.preview) {
          const reason = response.reason ?? "Zone operation was refused";
          if (/stale topology revision/i.test(reason)) await this.handleStaleZoneOperation(source, setError);
          else setError(reason);
          return;
        }
        if (!applyNow) {
          previewResult = response.preview;
          if (!Number.isSafeInteger(response.preview.revision) || response.preview.revision == null || response.preview.revision < 0) {
            previewResult = null;
            setError("Preview did not include a valid topology revision");
            return;
          }
          info.textContent = previewSummary(response.preview) + (response.preview.source_empties ? " · source will be empty" : "");
          apply.disabled = response.preview.collision_count > 0;
          return;
        }
        this.zoneOperationBusy = true;
        this.renameZoneButton.disabled = true;
        this.moveZoneButton.disabled = true;
        this.closeZoneDialog(false);
        this.exitSelectionMode();
        try {
          const activeMapSet = response.active_map_set ?? response.forked_to ?? this.mapSetID;
          await this.refreshAfterZoneOperation(activeMapSet, source, targetZone, response.preview.source_empties);
          if (!this.disposed) this.setStatus(kind === "move" ? "Rooms moved" : "Zone renamed", { autoHide: true });
        } finally {
          this.zoneOperationBusy = false;
          this.renameZoneButton.disabled = !this.currentZone || this.config.sessionID == null;
          this.setSelection(this.selectedCoords);
        }
      } catch (err) {
        if (controller.signal.aborted || !latest(generation)) return;
        const message = err instanceof Error ? err.message : String(err);
        if (/stale topology revision/i.test(message)) await this.handleStaleZoneOperation(source, setError);
        else setError(message);
      } finally {
        if (!latest(generation)) return;
        applying = false;
        preview.disabled = false;
        if (previewResult && !apply.disabled) apply.disabled = false;
      }
    };
    search.addEventListener("input", () => { renderOptions(); invalidate(); });
    existing.addEventListener("change", () => { newName.value = ""; invalidate(); });
    newName.addEventListener("input", invalidate);
    preview.addEventListener("click", () => void request(false));
    apply.addEventListener("click", () => { if (!applying) void request(true); });
    cancel.addEventListener("click", () => this.closeZoneDialog());
    backdrop.addEventListener("mousedown", (event) => { if (event.target === backdrop) this.closeZoneDialog(); });
    dialog.addEventListener("mousedown", (event) => {
      if (!dialog.open || event.target !== dialog) return;
      const rect = dialog.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)
        this.closeZoneDialog();
    });
    dialog.addEventListener("cancel", (event) => { event.preventDefault(); this.closeZoneDialog(); });
    dialog.addEventListener("keydown", (event) => {
      if (event.key === "Escape") { event.preventDefault(); this.closeZoneDialog(); return; }
      if (event.key !== "Tab") return;
      const focusable = [...dialog.querySelectorAll<HTMLElement>("button:not([disabled]), input, select")];
      const first = focusable[0], last = focusable.at(-1);
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    });
    this.zoneDialog = backdrop;
    this.root.appendChild(backdrop);
    if (typeof dialog.showModal === "function") {
      try { dialog.showModal(); } catch { /* regular positioned-dialog fallback */ }
    }
    window.setTimeout(() => newName.focus(), 0);
  }

  private closeZoneDialog(returnFocus = true): void {
    this.zoneRequest?.abort();
    this.zoneRequest = null;
    this.zoneRequestGeneration++;
    this.zoneListRequest?.abort();
    this.zoneListRequest = null;
    this.zoneListGeneration++;
    const dialog = this.zoneDialog?.querySelector<HTMLDialogElement>("dialog");
    if (dialog?.open) dialog.close();
    this.zoneDialog?.remove();
    this.zoneDialog = null;
    if (returnFocus) this.zoneDialogOpener?.focus();
    this.zoneDialogOpener = null;
  }

  private async handleStaleZoneOperation(source: string, setError: (message: string) => void): Promise<void> {
    setError("Topology changed; preview again.");
    // Fetch exactly the currently displayed zone once. Form fields and the
    // canvas-owned selection remain untouched, so retry does not lose operator work.
    if (this.currentZone !== source) return;
    const generation = this.nextLoadGeneration();
    this.zoneCache.delete(source);
    await this.showZone(source, { resetView: false }, generation);
  }

  private async refreshAfterZoneOperation(
    mapSetID: number | null,
    source: string,
    target: string,
    sourceEmpties: boolean,
  ): Promise<void> {
    if (mapSetID == null) return;
    this.mapSetID = mapSetID; // COW reply is authoritative before any zone fetch.
    this.zoneCache.clear();
    const destination = sourceEmpties && this.currentZone === source ? target : this.currentZone;
    await this.loadMapSet({
      mapSetID,
      preferredZone: destination,
      preserveView: destination === this.currentZone && !sourceEmpties,
      applyPosition: false,
    });
  }

  // --- live position following ------------------------------------------

  // Handle a room_position WS broadcast. Updates the confidence badge and, when
  // the position is valid, the marker + (if following) the view.
  async onRoomPosition(pos: PlayerPosition): Promise<void> {
    const prior = this.lastPosition;
    const repeatedCoord = prior?.valid && pos.valid &&
      prior.zone === pos.zone && prior.x === pos.x && prior.y === pos.y && prior.l === pos.l;
    this.lastPosition = pos;
    this.updateConfidence(pos);
    if (!pos.valid) {
      // Lost/red: keep the last marker where it was, just recolor via confidence.
      // The candidate cell is dropped — there is no coord to offer recording at.
      this.refreshGhost();
      return;
    }
    await this.applyPosition(
      pos,
      // ReloadActiveMapSet/WriteTopology replay the unchanged tracker snapshot.
      // Follow reacts to movement, not to that administrative re-broadcast.
      this.followToggle.checked && !repeatedCoord,
    );
    this.refreshGhost();
  }

  // Tell the pane which door-opening command this session's ROOM FORMAT supplies,
  // from the `mapper_profile` message. It changes what the edge editor PROMISES a
  // route will send, so a pane already showing the editor is closed rather than left
  // displaying the previous mud's verb.
  setDoorOpenTemplate(tmpl: string): void {
    if (tmpl === this.doorOpen) return;
    this.doorOpen = tmpl;
    this.closeCellMenu();
  }

  // Handle a `room` WS broadcast (the live room block). It carries no coords —
  // it only says WHAT the player is looking at — so it never moves the marker;
  // it enables the record action and labels the candidate cell.
  onRoomEvent(live: LiveRoom): void {
    this.liveRoom = live;
    this.refreshGhost();
  }

  // Handle server-owned map-walk progress. This is intentionally independent of
  // room_position: a page may restore while a hop is in flight, before another
  // room event arrives, and must still offer Stop.
  onMapWalk(next: MapWalkState): void {
    if (next.epoch && next.epoch < this.walkEpoch) return;
    if (next.epoch && next.epoch > this.walkEpoch) {
      this.walkEpoch = next.epoch;
      this.walkTerminalJobID = 0;
      this.walkSeenJobID = 0;
      this.walk = { active: false, current: 0, total: 0, state: "stopped" };
    }
    const jobID = next.job_id ?? 0;
    if (jobID > 0 && jobID < this.walkSeenJobID) return;
    if (jobID > this.walkSeenJobID) this.walkSeenJobID = jobID;
	if (!next.active && jobID === 0 && this.walk.active && (this.walk.job_id ?? 0) > 0) return;
    if (next.active && jobID > 0 && jobID <= this.walkTerminalJobID) return;
	if (
	  next.active && this.walk.active && jobID > 0 && jobID === this.walk.job_id &&
	  (next.revision ?? 0) < (this.walk.revision ?? 0)
	) return;
    if (
      next.active && this.walk.active && jobID > 0 && jobID === this.walk.job_id &&
      (next.current < this.walk.current || (this.walk.state === "stopping" && next.state === "running"))
    ) return;
    if (!next.active && jobID > 0) {
      this.walkTerminalJobID = Math.max(this.walkTerminalJobID, jobID);
    }
    this.walk = next;
    this.walkProgress.hidden = !next.active;
    this.walkStopButton.hidden = !next.active;
    this.walkResumeButton.hidden = !next.active || next.state !== "paused";
    this.walkResumeButton.disabled = !next.can_resume;
    this.walkStopButton.disabled = !next.active || next.state === "stopping";
    if (!next.active) {
      this.walkProgress.textContent = "";
      if (next.reason && next.reason !== "completed") this.setStatus(next.reason);
      return;
    }
    const hop = next.total > 0 ? Math.min(next.current + 1, next.total) : 0;
    if (next.state === "paused") {
      this.walkProgress.textContent = `Walk ${next.current}/${next.total}: ${next.reason ?? "paused"}`;
    } else {
      this.walkProgress.textContent = next.state === "stopping"
        ? `Stopping ${next.current}/${next.total}`
        : `Walk ${hop}/${next.total}`;
    }
  }

  private async cancelMapWalk(): Promise<void> {
    if (this.config.sessionID == null || !this.walk.active) return;
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-walk/cancel`,
        { method: "POST" },
      );
      if (!res.ok) throw new Error(await res.text());
      const state: MapWalkState = await res.json();
      this.onMapWalk(state);
    } catch (err) {
      this.setStatus(
        `Stop failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  private async resumeMapWalk(): Promise<void> {
    if (this.config.sessionID == null || !this.walk.active || this.walk.state !== "paused" || !this.walk.can_resume) return;
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-walk/resume`,
        { method: "POST" },
      );
      if (!res.ok) throw new Error(await res.text());
      this.onMapWalk(await res.json() as MapWalkState);
    } catch (err) {
      this.setStatus(`Continue failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  // Recompute the candidate (ghost) cell from the current position + the rooms
  // loaded for the displayed zone. Called after every position/zone/live-room
  // change; setGhost no-ops when nothing actually changed.
  private refreshGhost(): void {
    this.renderer.setGhost(this.ghostForState());
    // The record button reads the same three inputs (position, live room, the
    // zone's rooms), so it is refreshed from the same place — otherwise it would
    // need its own call at every one of these sites and would drift stale.
    this.updateRecordButton();
  }

  private ghostForState(): {
    x: number;
    y: number;
    l: number;
    color: string;
    label?: string;
  } | null {
    // Before the zone's rooms are loaded EVERY coord looks unoccupied, which
    // would flash a candidate outline over a real room. Only decide once we
    // actually have the zone's data.
    const rooms = this.zoneCache.get(this.currentZone);
    if (!rooms) return null;
    const cand = ghostCandidate(this.lastPosition, this.currentZone, rooms);
    if (!cand) return null;
    const pos = this.lastPosition!;
    return {
      x: cand.x,
      y: cand.y,
      l: cand.l,
      color: CONF_COLOR[pos.confidence] ?? CONF_COLOR.red,
      label: this.liveRoom?.hint || undefined,
    };
  }

  // Whether a room already occupies the cell the player stands in. Tri-state:
  // null when we cannot know yet (no position, or that zone is not fetched — the
  // player may well be in a zone other than the one on screen). Only the button's
  // wording depends on it, never on whether the write is allowed.
  private currentCellExists(): boolean | null {
    const pos = this.lastPosition;
    if (!pos?.valid) return null;
    const rooms = this.zoneCache.get(pos.zone);
    if (!rooms) return null;
    return rooms.some((r) => r.x === pos.x && r.y === pos.y && r.l === pos.l);
  }

  // Re-derive the top-bar record button from the current tracker/live state. The
  // button stays VISIBLE at all times and goes disabled with the reason in its
  // tooltip: in a new location the operator needs to see that recording exists
  // and why it is not available right now, not to find out by clicking.
  private updateRecordButton(): void {
    const st = recordButtonState(
      this.lastPosition,
      this.liveRoom,
      this.currentCellExists(),
    );
    this.recordButton.textContent = st.label;
    this.recordButton.disabled = !st.enabled;
    this.recordButton.title = st.title;
  }

  private async onRecordClick(): Promise<void> {
    // Re-derive at click time rather than trusting the last paint: a room_position
    // could have landed between the repaint and the click.
    const st = recordButtonState(
      this.lastPosition,
      this.liveRoom,
      this.currentCellExists(),
    );
    if (!st.enabled || !st.cell) {
      this.setStatus(st.title, { sticky: true });
      this.updateRecordButton();
      return;
    }
    await this.recordRoom(st.cell);
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
    seam: SeamTarget,
    _room: SlimRoom,
  ): Promise<void> {
    if (!seam.passable || seam.to == null) {
      this.setStatus(!seam.passable ? "Seam is blocked" : "Seam target is unresolved");
      return;
    }
    const target = findZoneName(seam.to.zone, this.zoneNames);
    if (!target) {
      this.setStatus(`Seam target zone "${seam.to.zone}" not found`);
      return;
    }
    const generation = this.nextLoadGeneration();
    const mapSetID = this.mapSetID;
    const rooms = await this.showZone(target, { resetView: true }, generation);
    if (
      rooms === null ||
      !this.isCurrentLoad(generation, mapSetID) ||
      this.currentZone !== target
    ) return;
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
  // `room` is null for the CANDIDATE (ghost) cell — a coord with no room in the
  // map yet — in which case only the record + undo actions make sense.
  private openCellMenu(
    cell: { z: string; x: number; y: number; l: number },
    room: SlimRoom | null,
    clientX: number,
    clientY: number,
  ): void {
    this.closeCellMenu();
    const pos = this.lastPosition;

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
        "Update cell from live state",
        () => void this.patchCellFromLive(pos!),
      );
      updateBtn.classList.add("map-cell-menu__update");
      menu.appendChild(updateBtn);
    }

    if (room) {
      this.appendSeamItems(menu, room);
      const portal = this.menuButton("Add directed portal", () =>
        this.openPortalMenu(room, null, clientX, clientY),
      );
      portal.classList.add("map-cell-menu__portal-add");
      portal.title = "Create a transition to an exact cell in this or another zone";
      menu.appendChild(portal);
    }

    // 2) «Записать комнату» — the human autorecord. One action, two labels:
    // create the cell (ghost) or refresh an existing one from the live text.
    const rec = recordAction(pos, this.liveRoom, cell, room !== null);
    // HARD INVARIANT: never offer a clickable write we cannot show the operator
    // first. recordAction already refuses a missing/blank live room, but tying
    // the button to the PREVIEW itself makes "no preview => no record" structural
    // rather than two conditions that could drift apart.
    const preview = recordPreview(this.liveRoom, room);
    const recEnabled = rec.enabled && preview !== null;
    const recReason =
      rec.reason ||
      (preview === null
        ? "no live room received yet — nothing to preview"
        : "");
    // Confirm-before-you-write: show the live name + the exits that will be
    // recorded, and for an update the diff against what the cell holds now.
    // Surplus exits are what can fabricate edges into unrelated neighbours, so
    // they must be visible BEFORE the click, not discovered via a bad route.
    if (recEnabled) {
      const pv = preview;
      if (pv) {
        const box = document.createElement("div");
        box.className = "map-cell-menu__preview";
        const lines = [`Will record: ${pv.hint}`];
        lines.push(`exits: ${pv.exits.join(" ") || "none"}`);
        const diff: string[] = [];
        if (pv.added.length > 0) diff.push(`+${pv.added.join("+")}`);
        if (pv.removed.length > 0) diff.push(`−${pv.removed.join("−")}`);
        if (diff.length > 0) lines.push(`changes: ${diff.join(" / ")}`);
        box.textContent = lines.join("\n");
        menu.appendChild(box);
      }
    }
    const recordBtn = this.menuButton(
      rec.label,
      () => void this.recordRoom(cell),
    );
    recordBtn.classList.add("map-cell-menu__record");
    if (!recEnabled) {
      recordBtn.disabled = true;
      recordBtn.title = recReason;
    }
    menu.appendChild(recordBtn);

    // 3) "Идти сюда" — insert into chat OR send to server. A candidate cell has
    // no room in the map, so there is nothing to route to yet.
    if (room) {
      this.appendRouteItems(menu, cell, room, pos);
    }

    // 4) Hand-drawn VERTICAL marks (U/D). The boundary popup cannot reach these:
    // a floor plan has no boundary between a cell and the cell above it, so a U/D
    // mark is drawn only as a corner badge. It still has to be REMOVABLE — a mark
    // that cannot be taken back from the UI is a trap — so the cell menu lists
    // whatever is on those two faces and offers to clear it. Making U/D marks with
    // the mouse is a separate surface and is NOT here (MCP does it today).
    if (room) {
      this.appendVerticalMarkItems(menu, cell, room);
    }

    // 5) «Я здесь» + undo. Anchoring needs a real room; undo is always offered
    // (this surface is experimental — every write must be reversible from here).
    if (room) {
      const anchorBtn = this.menuButton(
        "I'm here",
        () => void this.anchorHere(room),
      );
      anchorBtn.classList.add("map-cell-menu__anchor");
      menu.appendChild(anchorBtn);
    }
    const undoBtn = this.menuButton(
      "Undo last map change",
      () => void this.undoMapWrite(),
    );
    undoBtn.classList.add("map-cell-menu__undo");
    menu.appendChild(undoBtn);

    this.placeCellMenu(menu, clientX, clientY);

    // Dismiss on outside-click / Esc. Defer the outside-click listener a tick so
    // the click that opened the menu does not immediately close it.
    setTimeout(() => {
      if (this.disposed || this.cellMenu !== menu) return;
      document.addEventListener("mousedown", this.onOutsideMenuClick, true);
      document.addEventListener("keydown", this.onMenuKeydown, true);
    }, 0);
  }

  // Cross-zone transitions are direct room-menu actions. The menu already has a
  // pane-relative max height, so every valid seam remains reachable by keyboard.
  private appendSeamItems(menu: HTMLElement, room: SlimRoom): void {
    const seams = seamsOf(room);
    if (seams.length === 0) return;

    const section = document.createElement("div");
    section.className = "map-cell-menu__seams";
    for (const seam of seams) {
      const row = document.createElement("div");
      row.className = "map-cell-menu__seam-row";
      const button = this.menuButton(
        `${seam.command || "transition"} → ${seam.zone}`,
        () => void this.jumpSeam(seam, room),
      );
      button.classList.add("map-cell-menu__seam-item");
      button.disabled = !seam.passable || seam.to == null;
      button.title = button.disabled
        ? (!seam.passable ? "Transition is blocked" : "Target room is unresolved")
        : `Target tag: ${seam.tag}`;
      row.appendChild(button);
      const props = document.createElement("button");
      props.type = "button";
      props.className = "map-cell-menu__seam-props";
      props.textContent = "⚙";
      props.setAttribute("aria-label", `Transition properties for ${seam.command || "transition"} to ${seam.zone}`);
      props.title = "Transition properties";
      props.disabled = seam.tag == null || seam.zone === "";
      if (props.disabled) props.title = "Incomplete legacy selector: repair the exact portal target";
      props.addEventListener("click", (event) => {
        const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
        this.openSeamMenu(room, seam, rect.left, rect.bottom);
      });
      row.appendChild(props);
      if (props.disabled) {
        const repair = document.createElement("button");
        repair.type = "button";
        repair.className = "map-cell-menu__portal-repair";
        repair.textContent = "Repair target";
        repair.title = "Assign an exact target cell and convert the legacy transition into a portal";
        repair.addEventListener("click", (event) => {
          const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
          this.openPortalMenu(room, seam, rect.left, rect.bottom);
        });
        row.appendChild(repair);
      }
      section.appendChild(row);
    }
    menu.appendChild(section);
  }

  // A seam's amber button stays an immediate cross-zone navigation action. Its
  // properties are deliberately a separate compact control: these attributes
  // describe the directed transition, not the zone the operator wants to view.
  private openSeamMenu(room: SlimRoom, seam: SeamTarget, clientX: number, clientY: number): void {
    this.openPortalMenu(room, seam, clientX, clientY);
  }

  // The normalized seam list carries the source occurrence order and resolved
  // exact endpoint. That lets a dead imported seam be repaired without relying
  // on its legacy zone/tag selector, and keeps duplicate commands independent.
  private openPortalMenu(room: SlimRoom, seam: SeamTarget | null, clientX: number, clientY: number): void {
    this.closeCellMenu();
    const menu = document.createElement("div");
    menu.className = "map-cell-menu map-edge-menu";
    menu.setAttribute("role", "menu");
    const header = document.createElement("div");
    header.className = "map-cell-menu__header";
    header.textContent = seam ? `Transition: ${seam.command || "transition"} → ${seam.zone}` : "New directed portal";
    menu.appendChild(header);

    const box = document.createElement("div");
    box.className = "map-edge-menu__side";
    const title = document.createElement("div");
    title.className = "map-cell-menu__section";
    title.textContent = `from '${room.h || "cell"}' (${room.z} ${room.x},${room.y},${room.l})`;
    box.appendChild(title);
    const attrs = this.appendDirectedAttrControls(box, seam ? seamAttrs(room, seam) : { weight: 1, phrase: "", door: false, locked: false, openCmd: "", noRoute: false }, seamDirection(seam?.command ?? ""), (patch) =>
      seam
        ? (seam.tag == null ? this.writePortalAttrs(room, seam, patch) : this.writeSeamAttrs(room, seam, patch))
        : Promise.resolve(false),
    );
    const details = document.createElement("div");
    details.className = "map-edge-menu__portal-details";
    const command = document.createElement("input");
    command.type = "text";
    command.value = seam?.command ?? "";
    command.placeholder = "Transition command";
    command.className = "map-edge-menu__input";
    details.appendChild(command);

    const zone = document.createElement("select");
    zone.className = "map-edge-menu__input";
    for (const name of this.zoneNames) {
      const option = document.createElement("option");
      option.value = name;
      option.textContent = name;
      zone.appendChild(option);
    }
    zone.value = seam?.to?.zone ?? room.z;
    details.appendChild(zone);

    const targetSearch = document.createElement("input");
    targetSearch.type = "search";
    targetSearch.className = "map-edge-menu__input";
    targetSearch.placeholder = "Search by name or coordinates";
    details.appendChild(targetSearch);
    const targetRoom = document.createElement("select");
    targetRoom.className = "map-edge-menu__input";
    const x = document.createElement("input"), y = document.createElement("input"), l = document.createElement("input");
    for (const input of [x, y, l]) { input.type = "number"; input.className = "map-edge-menu__input"; }
    x.value = String(seam?.to?.x ?? room.x); y.value = String(seam?.to?.y ?? room.y); l.value = String(seam?.to?.l ?? room.l);
    let targetRequest = 0;
    let targetAbort: AbortController | null = null;
    let targetCandidates: SlimRoom[] = [];
    const renderTargets = (): void => {
      const needle = targetSearch.value.trim().toLocaleLowerCase();
      targetRoom.replaceChildren();
      for (const candidate of targetCandidates) {
        const searchable = `${candidate.h} ${candidate.x},${candidate.y},${candidate.l}`.toLocaleLowerCase();
        if (needle && !searchable.includes(needle)) continue;
        const option = document.createElement("option");
        option.value = `${candidate.x},${candidate.y},${candidate.l}`;
        option.textContent = `${candidate.h || "cell"} (${candidate.x},${candidate.y},${candidate.l})`;
        if (candidate.x === Number(x.value) && candidate.y === Number(y.value) && candidate.l === Number(l.value)) option.selected = true;
        targetRoom.appendChild(option);
      }
    };
    const populateTargets = async (): Promise<void> => {
      const request = ++targetRequest;
      const requestedZone = zone.value;
      targetAbort?.abort();
      const controller = new AbortController();
      targetAbort = controller;
      targetRoom.innerHTML = "";
      try {
        const rooms = await this.fetchZone(requestedZone, this.loadGeneration, this.mapSetID, controller.signal);
        if (rooms == null || request !== targetRequest || zone.value !== requestedZone || this.cellMenu !== menu) return;
        targetCandidates = rooms;
        renderTargets();
      } catch (err) {
        if (controller.signal.aborted || request !== targetRequest || this.cellMenu !== menu) return;
        this.setStatus(`Failed to load target zone: ${err instanceof Error ? err.message : String(err)}`, { sticky: true });
      }
    };
    targetSearch.addEventListener("input", renderTargets);
    targetRoom.addEventListener("change", () => { const [tx, ty, tl] = targetRoom.value.split(",").map(Number); x.value = String(tx); y.value = String(ty); l.value = String(tl); });
    zone.addEventListener("change", () => void populateTargets());
    details.append(targetRoom, x, y, l);
    void populateTargets();

    const save = this.menuButton(seam ? "Save portal" : "Create portal", () => void this.writePortal(room, seam, {
      command: command.value, target: { zone: zone.value, x: Number(x.value), y: Number(y.value), l: Number(l.value) },
    }));
    details.appendChild(save);
    if (seam) {
      const remove = this.menuButton("Delete portal", () => {
        if (window.confirm("Delete this directed portal?")) void this.deletePortal(room, seam);
      });
      remove.classList.add("map-edge-menu__delete");
      details.appendChild(remove);
    }
    box.appendChild(details);
    menu.appendChild(box);

    this.placeCellMenu(menu, clientX, clientY);
    this.cellMenuValid = () => {
      const current = this.zoneCache.get(room.z)?.find(
        (candidate) => candidate.x === room.x && candidate.y === room.y && candidate.l === room.l,
      );
      const present = seam != null && current != null && seamsOf(current).some(
        (candidate) => candidate.order === seam.order,
      );
      if (present && current && seam) attrs.sync(seamAttrs(current, seam));
      return seam == null || present;
    };
    setTimeout(() => {
      if (this.disposed || this.cellMenu !== menu) return;
      document.addEventListener("mousedown", this.onOutsideMenuClick, true);
      document.addEventListener("keydown", this.onMenuKeydown, true);
    }, 0);
  }

  // The hand-drawn U/D block of the cell menu. Only rendered when the cell
  // actually carries a vertical mark, so the menu does not grow for the 99.9% of
  // cells that have none.
  private appendVerticalMarkItems(
    menu: HTMLElement,
    cell: Cell,
    room: SlimRoom,
  ): void {
    const marks = verticalMarks(room);
    if (marks.length === 0) return;
    const header = document.createElement("div");
    header.className = "map-cell-menu__section";
    header.textContent = "Manual up/down marks";
    menu.appendChild(header);
    for (const mk of marks) {
      const label = document.createElement("div");
      label.className = "map-edge-menu__prov";
      label.textContent = `${dirName(mk.dir)}: ${provenanceLabel(mk.state)}`;
      menu.appendChild(label);
      const btn = this.menuButton(`Clear mark (${dirName(mk.dir)})`, () =>
        void this.writeEdgeMark(
          {
            from: cell,
            to: { z: cell.z, x: cell.x, y: cell.y, l: cell.l + (mk.dir === "U" ? 1 : -1) },
            dir: mk.dir,
            fromLabel: room.h || "cell",
            toLabel: mk.dir === "U" ? "floor above" : "floor below",
            state: mk.state,
            passable: mk.state === "open",
            provenance: provenanceLabel(mk.state),
          },
          "auto",
        ),
      );
      btn.classList.add("map-edge-menu__clear");
      menu.appendChild(btn);
    }
  }

  // The route ("Идти сюда") block of the cell menu — only for real rooms.
  private appendRouteItems(
    menu: HTMLElement,
    cell: { z: string; x: number; y: number; l: number },
    room: SlimRoom,
    pos: PlayerPosition | null,
  ): void {
    const goHeader = document.createElement("div");
    goHeader.className = "map-cell-menu__section";
    goHeader.textContent = `Go here: ${room.h || "cell"}`;
    menu.appendChild(goHeader);

    const routable = canRoute(pos) && !isCurrentCell(pos, cell);
    const insertBtn = this.menuButton(
      "Insert route into chat",
      () => void this.routeToCell(cell, room, "insert"),
    );
    const sendBtn = this.menuButton(
      "Start route",
      () => void this.routeToCell(cell, room, "send"),
    );
    if (!routable) {
      insertBtn.disabled = true;
      sendBtn.disabled = true;
      const why = isCurrentCell(pos, cell)
        ? "you are already here"
        : "no position — use 'I'm here' first";
      insertBtn.title = why;
      sendBtn.title = why;
    }
    menu.appendChild(insertBtn);
    menu.appendChild(sendBtn);
  }

  // --- edge editor (the popup on a cell boundary) ------------------------

  // Open the edge editor for the boundary between `cell` and its grid neighbour
  // in `dir`. It reuses the cell menu's container, placement, dismissal and
  // lifetime (`this.cellMenu`) on purpose: only one popup may be open at a time,
  // and the cell menu already solves clamping inside the pane, Esc and
  // outside-click. It is a DIFFERENT surface, not a section of that menu — see
  // edgeEditor.ts for why.
  //
  // Layout, one block per DIRECTION:
  //
  //   [заголовок: К1 ↔ К2]
  //   ┌ из «К1» в «К2» ────────────────┐
  //   │ [x] есть проход                │
  //   │ проход разобран из текста      │   <- provenance, never guessed
  //   │ (future controls: trigger words, door/lock land HERE)
  //   └────────────────────────────────┘
  //   ┌ из «К2» в «К1» ────────────────┐ ... same
  //
  // The two blocks are independent writes. A trigger phrase and door/lock flags
  // are per-direction too (a passage can need "двигать ящик" going down and
  // "лезть ковёр" going up), so they slot in as further controls inside the SAME
  // block, under the checkbox, with no change to this structure or to the popup's
  // two-block shape.
  private openEdgeMenu(
    cell: Cell,
    dir: string,
    clientX: number,
    clientY: number,
  ): void {
    this.closeCellMenu();
    const model = this.edgeModel(cell, dir);
    if (!model) return;

    const menu = document.createElement("div");
    menu.className = "map-cell-menu map-edge-menu";
    menu.setAttribute("role", "menu");

    const header = document.createElement("div");
    header.className = "map-cell-menu__header";
    header.textContent = `Edge: ${model.sides[0].fromLabel} ↔ ${model.sides[0].toLabel}`;
    menu.appendChild(header);

    const blocks: EdgeSideBlock[] = [];
    for (const side of model.sides) {
      const block = this.edgeSideBlock(side);
      blocks.push(block);
      menu.appendChild(block.element);
    }

    const hint = document.createElement("div");
    hint.className = "map-edge-menu__hint";
    hint.textContent =
      "This mark belongs to the edge, not the room exits: recording the room will not erase it. " +
      "Use 'Undo last map change' in the cell menu to restore the previous state.";
    menu.appendChild(hint);

    this.placeCellMenu(menu, clientX, clientY);
    this.cellMenuValid = () => {
      if (this.currentZone !== cell.z) return false;
      const current = this.edgeModel(cell, dir);
      if (!current) return false;
      if (current.sides.some(
        (side, index) => side.attrsEditable !== (blocks[index]?.attrs != null),
      )) return false;
      current.sides.forEach((side, index) => blocks[index]?.attrs?.sync(side.attrs));
      return true;
    };
    setTimeout(() => {
      if (this.disposed || this.cellMenu !== menu) return;
      document.addEventListener("mousedown", this.onOutsideMenuClick, true);
      document.addEventListener("keydown", this.onMenuKeydown, true);
    }, 0);
  }

  // Build the popup model from the rooms the pane already has for this zone.
  private edgeModel(cell: Cell, dir: string): EdgeModel | null {
    const rooms = this.zoneCache.get(this.currentZone);
    if (!rooms) return null;
    const at = (c: Cell | null): SlimRoom | null =>
      c
        ? (rooms.find((r) => r.x === c.x && r.y === c.y && r.l === c.l) ?? null)
        : null;
    const model = buildEdgeModel(cell, dir, at(cell), null);
    if (!model) return null;
    // Re-resolve with the neighbour now that buildEdgeModel computed its coord.
    return buildEdgeModel(cell, dir, at(model.a), at(model.b));
  }

  // One directed half of the boundary.
  private edgeSideBlock(side: EdgeSide): EdgeSideBlock {
    const box = document.createElement("div");
    box.className = "map-edge-menu__side";

    const title = document.createElement("div");
    title.className = "map-cell-menu__section";
    title.textContent = `from '${side.fromLabel}' to '${side.toLabel}' (${dirName(side.dir)})`;
    box.appendChild(title);

    const label = document.createElement("label");
    label.className = "map-edge-menu__toggle";
    const box_ = document.createElement("input");
    box_.type = "checkbox";
    box_.checked = side.passable;
    box_.addEventListener("change", () => {
      const mark = markFor(side.state, box_.checked);
      if (mark === null) return;
      void this.writeEdgeMark(side, mark);
    });
    label.appendChild(box_);
    const text = document.createElement("span");
    text.textContent = "passage exists";
    label.appendChild(text);
    box.appendChild(label);

    const prov = document.createElement("div");
    prov.className = "map-edge-menu__prov";
    prov.textContent = side.provenance;
    box.appendChild(prov);

    // The ATTRIBUTE controls, under the "есть проход" checkbox and inside this
    // direction's block, because every one of them is one-sided.
    const attrs = this.appendEdgeAttrControls(box, side);

    // Only offered when a hand-drawn row is what decides the face: "let the room's
    // own exits decide again" is meaningless where nothing was drawn by hand.
    if (canClear(side.state)) {
      const clearBtn = this.menuButton("Clear manual mark", () =>
        void this.writeEdgeMark(side, "auto"),
      );
      clearBtn.classList.add("map-edge-menu__clear");
      box.appendChild(clearBtn);
    }
    return { element: box, attrs };
  }

  // The three attributes of ONE directed face: the traversal phrase, the door
  // (+ lock + opening command) and the routing veto. Rendered inside the direction
  // block rather than once per boundary, because a phrase, a lock and a veto are
  // all statements about the side you approach from — the same reason the rows are
  // directed in the first place.
  //
  // A face with no passage gets no controls at all: attributes describe HOW a
  // passage is taken, so there is nothing to describe, and the backend refuses the
  // write. Saying that once beats offering inputs that error on blur.
  private appendEdgeAttrControls(box: HTMLElement, side: EdgeSide): AttrControlBinding | null {
    if (!side.attrsEditable) return null;
    return this.appendDirectedAttrControls(box, side.attrs, side.dir, (patch) =>
      this.writeEdgeAttrs(side, patch),
    );
  }

  private appendDirectedAttrControls(
    box: HTMLElement,
    a: EdgeAttrs,
    dir: string,
    write: (patch: AttrPatch) => Promise<boolean>,
  ): AttrControlBinding {
    const controls = document.createElement("div");
    controls.className = "map-edge-menu__attrs";
    box.appendChild(controls);
    const state: {
      base: EdgeAttrs;
      attrs: EdgeAttrs;
      pending: boolean;
      pendingPatch: AttrPatch | null;
      writeID: number;
      focusKey: string | null;
    } = {
      base: { ...a }, attrs: { ...a }, pending: false, pendingPatch: null,
      writeID: 0, focusKey: null,
    };

    const submit = (patch: AttrPatch): void => {
      if (state.pending) return;
      state.pendingPatch = patch;
      state.attrs = applyAttrPatch(state.base, patch);
      state.pending = true;
      const writeID = ++state.writeID;
      render();
      void write(patch).then(
        (ok) => {
          if (writeID !== state.writeID) return;
          state.pending = false;
          if (ok) state.base = applyAttrPatch(state.base, patch);
          state.pendingPatch = null;
          state.attrs = state.base;
          render();
        },
        () => {
          if (writeID !== state.writeID) return;
          state.pending = false;
          state.pendingPatch = null;
          state.attrs = state.base;
          render();
        },
      );
    };

    const render = (): void => {
      const active = document.activeElement as HTMLElement | null;
      if (active && controls.contains(active) && active.dataset.mapEdgeAttr) {
        state.focusKey = active.dataset.mapEdgeAttr;
      }
      controls.replaceChildren();
      const attrs = state.attrs;
      controls.appendChild(this.attrNumber("Route weight", attrs.weight, (v) => submit({ weight: v }), "weight"));
      controls.appendChild(this.attrText(
        "traversal phrase", attrs.phrase, "for example: move crate", (v) => submit({ phrase: v }), "phrase",
      ));
      controls.appendChild(this.attrCheck("door", attrs.door, (v) => submit({ door: v }), "door"));
      // The lock and opening command are meaningful only with a door, so an
      // optimistic door toggle reveals them in the same popup before its write settles.
      if (attrs.door) {
        controls.appendChild(this.attrCheck(
          "locked by default", attrs.locked, (v) => submit({ locked: v }), "locked",
        ));
        controls.appendChild(this.attrText(
          "opening command", attrs.openCmd, this.openCmdPlaceholder(dir), (v) => submit({ open_cmd: v }), "open_cmd",
        ));
      }
      controls.appendChild(this.attrCheck(
        "avoid in routes", attrs.noRoute, (v) => submit({ no_route: v }), "no_route",
      ));

      const summary = attrsSummary(attrs, dir, this.doorOpen);
      if (summary) {
        const line = document.createElement("div");
        line.className = "map-edge-menu__prov";
        line.textContent = summary;
        controls.appendChild(line);
      }
      controls.setAttribute("aria-busy", String(state.pending));
      this.updateAttrMenuPending(box);
      const menu = box.closest<HTMLElement>(".map-edge-menu");
      if (menu && menu === this.cellMenu && this.cellMenuAnchor) {
        this.placeCellMenu(menu, this.cellMenuAnchor.clientX, this.cellMenuAnchor.clientY);
      }
      if (!state.pending && state.focusKey) {
        controls.querySelector<HTMLElement>(`[data-map-edge-attr="${state.focusKey}"]`)?.focus();
        state.focusKey = null;
      }
    };
    render();
    return {
      sync(attrs: EdgeAttrs): void {
        state.base = { ...attrs };
        state.attrs = state.pendingPatch
          ? applyAttrPatch(state.base, state.pendingPatch)
          : state.base;
        render();
      },
    };
  }

  private updateAttrMenuPending(box: HTMLElement): void {
    const scope = box.closest<HTMLElement>(".map-edge-menu") ?? box;
    const busy = scope.querySelector('.map-edge-menu__attrs[aria-busy="true"]') !== null;
    scope.setAttribute("aria-busy", String(busy));
    const controls = scope.querySelectorAll<
      HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement
    >("input, button, select, textarea");
    if (busy) {
      for (const control of controls) {
        if (!control.disabled) {
          control.disabled = true;
          control.dataset.mapAttrsPendingDisabled = "true";
        }
      }
      return;
    }
    for (const control of controls) {
      if (control.dataset.mapAttrsPendingDisabled === "true") {
        control.disabled = false;
        delete control.dataset.mapAttrsPendingDisabled;
      }
    }
  }

  // A labelled free-text attribute. It commits on blur / Enter and only when the
  // value actually changed, so opening the popup and closing it writes nothing and
  // pushes nothing onto the undo stack.
  private attrText(
    label: string,
    value: string,
    placeholder: string,
    commit: (v: string) => void,
    field?: string,
  ): HTMLElement {
    const wrap = document.createElement("label");
    wrap.className = "map-edge-menu__attr";
    const cap = document.createElement("span");
    cap.textContent = label;
    wrap.appendChild(cap);
    const input = document.createElement("input");
    input.type = "text";
    if (field) input.dataset.mapEdgeAttr = field;
    input.value = value;
    input.placeholder = placeholder;
    let committed = false;
    const fire = (): void => {
      if (committed) return;
      const v = input.value.trim();
      if (v === value) return;
      committed = true;
      commit(v);
    };
    input.addEventListener("blur", fire);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        fire();
      }
    });
    wrap.appendChild(input);
    return wrap;
  }

  // Routing weight accepts fractional costs, but a malformed/zero value must
  // remain local: the server has the same validation at its write boundary.
  private attrNumber(label: string, value: number, commit: (v: number) => void, field?: string): HTMLElement {
    const wrap = document.createElement("label");
    wrap.className = "map-edge-menu__attr";
    const cap = document.createElement("span");
    cap.textContent = label;
    wrap.appendChild(cap);
    const input = document.createElement("input");
    input.type = "number";
    if (field) input.dataset.mapEdgeAttr = field;
    input.min = "0";
    input.step = "any";
    input.value = String(value);
    let committedValue = value;
    const fire = (): void => {
      const next = Number(input.value);
      if (!Number.isFinite(next) || next <= 0) {
        input.setCustomValidity("Weight must be greater than zero");
        input.reportValidity();
        return;
      }
      input.setCustomValidity("");
      if (next !== committedValue) {
        committedValue = next;
        commit(next);
      }
    };
    input.addEventListener("blur", fire);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        fire();
      }
    });
    wrap.appendChild(input);
    return wrap;
  }

  // The placeholder of "команда открытия": what a route WOULD send if the operator
  // leaves the field empty. When the room format describes no door verb there is no
  // such command, and the placeholder says that rather than inviting the operator to
  // expect one.
  private openCmdPlaceholder(dir: string): string {
    if (!dir) return "enter an opening command";
    return this.doorOpen
      ? renderDefaultOpen(this.doorOpen, dir)
      : "the room format does not open doors — without a command the route sends nothing";
  }

  private attrCheck(
    label: string,
    value: boolean,
    commit: (v: boolean) => void,
    field?: string,
  ): HTMLElement {
    const wrap = document.createElement("label");
    wrap.className = "map-edge-menu__toggle";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    if (field) cb.dataset.mapEdgeAttr = field;
    cb.checked = value;
    cb.addEventListener("change", () => commit(cb.checked));
    wrap.appendChild(cb);
    const text = document.createElement("span");
    text.textContent = label;
    wrap.appendChild(text);
    return wrap;
  }

  // POST one face's attribute patch. Only the control that moved is sent, so the two
  // halves of a door — or two people editing them — cannot clobber each other's
  // fields (the backend treats an omitted attribute as "leave it alone").
  private async writeEdgeAttrs(
    side: EdgeSide,
    patch: AttrPatch,
  ): Promise<boolean> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot edit the edge");
      return false;
    }
    const key = JSON.stringify(["grid", side.from.z, side.from.x, side.from.y, side.from.l, side.dir]);
    return this.enqueueAttrWrite(key, async (isLatest) => {
      try {
        const res = await fetchWithToken(
          `/api/sessions/${this.config.sessionID}/map-edge-attrs`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              zone: side.from.z,
              x: side.from.x,
              y: side.from.y,
              l: side.from.l,
              dir: side.dir,
              ...patch,
            }),
          },
        );
        if (!res.ok) throw new Error(await res.text());
        const data: { ok?: boolean; reason?: string; forked_to?: number } =
          await res.json();
        if (!data.ok) {
          if (isLatest()) this.setStatus(`Failed to save edge attribute: ${data.reason ?? "?"}`, {
            sticky: true,
          });
          return false;
        }
        let status = `${side.fromLabel} → ${side.toLabel}: ${describeAttrPatch(patch, side.dir, this.doorOpen)}`;
        if (data.forked_to != null) status += " (map set forked to an editable copy)";
        if (isLatest()) this.setStatus(`${status} · use Undo in the cell menu to restore it`, {
          sticky: true,
        });
        return true;
      } catch (err) {
        if (isLatest()) this.setStatus(
          `Edge attribute failed: ${err instanceof Error ? err.message : String(err)}`,
        );
        return false;
      }
    });
  }

  private async writePortal(
    room: SlimRoom,
    seam: SeamTarget | null,
    value: { command: string; target: { zone: string; x: number; y: number; l: number } },
  ): Promise<void> {
    if (this.config.sessionID == null) { this.setStatus("No session — cannot edit the portal"); return; }
    const generation = ++this.portalWriteGeneration;
    const isLatest = (): boolean => !this.disposed && generation === this.portalWriteGeneration;
    try {
      const res = await fetchWithToken(`/api/sessions/${this.config.sessionID}/map-portals`, {
        method: seam ? "PATCH" : "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ zone: room.z, x: room.x, y: room.y, l: room.l, ...(seam ? { order: seam.order } : {}), command: value.command, target: value.target }),
      });
      if (!res.ok) throw new Error(await res.text());
      const data: { ok?: boolean; reason?: string; forked_to?: number } = await res.json();
      if (!data.ok) {
        if (isLatest()) this.setStatus(`Portal was not saved: ${data.reason ?? "?"}`, { sticky: true });
        return;
      }
      if (isLatest()) this.setStatus(`${seam ? "Portal updated" : "Portal created"}${data.forked_to != null ? " (map set forked to an editable copy)" : ""}`, { sticky: true });
    } catch (err) {
      if (isLatest()) this.setStatus(`Portal write failed: ${err instanceof Error ? err.message : String(err)}`, { sticky: true });
    }
  }

  private async deletePortal(room: SlimRoom, seam: SeamTarget): Promise<void> {
    if (this.config.sessionID == null) { this.setStatus("No session — cannot delete the portal"); return; }
    const generation = ++this.portalWriteGeneration;
    const isLatest = (): boolean => !this.disposed && generation === this.portalWriteGeneration;
    try {
      const res = await fetchWithToken(`/api/sessions/${this.config.sessionID}/map-portals`, {
        method: "DELETE", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ zone: room.z, x: room.x, y: room.y, l: room.l, order: seam.order }),
      });
      if (!res.ok) throw new Error(await res.text());
      const data: { ok?: boolean; reason?: string } = await res.json();
      if (!data.ok) {
        if (isLatest()) this.setStatus(`Portal was not deleted: ${data.reason ?? "?"}`, { sticky: true });
        return;
      }
      if (isLatest()) this.setStatus("Portal deleted", { sticky: true });
    } catch (err) {
      if (isLatest()) this.setStatus(`Portal delete failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  private async writePortalAttrs(room: SlimRoom, seam: SeamTarget, patch: AttrPatch): Promise<boolean> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot edit the portal");
      return false;
    }
    const key = JSON.stringify(["portal", room.z, room.x, room.y, room.l, seam.order]);
    return this.enqueueAttrWrite(key, async (isLatest) => {
      try {
        const res = await fetchWithToken(`/api/sessions/${this.config.sessionID}/map-portals`, {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            zone: room.z, x: room.x, y: room.y, l: room.l, order: seam.order, ...patch,
          }),
        });
        if (!res.ok) throw new Error(await res.text());
        const data: { ok?: boolean; reason?: string; forked_to?: number } = await res.json();
        if (!data.ok) {
          if (isLatest()) this.setStatus(`Failed to save portal attribute: ${data.reason ?? "?"}`, { sticky: true });
          return false;
        }
        let status = `${room.h || "cell"} → ${seam.zone}: ${describeAttrPatch(patch, seamDirection(seam.command), this.doorOpen)}`;
        if (data.forked_to != null) status += " (map set forked to an editable copy)";
        if (isLatest()) this.setStatus(`${status} · use Undo in the cell menu to restore it`, { sticky: true });
        return true;
      } catch (err) {
        if (isLatest()) this.setStatus(`Portal attribute failed: ${err instanceof Error ? err.message : String(err)}`);
        return false;
      }
    });
  }

  private async writeSeamAttrs(room: SlimRoom, seam: SeamTarget, patch: AttrPatch): Promise<boolean> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot edit the seam");
      return false;
    }
    const key = JSON.stringify([
      "seam", room.z, room.x, room.y, room.l, seam.order,
    ]);
    return this.enqueueAttrWrite(key, async (isLatest) => {
      try {
        const res = await fetchWithToken(
          `/api/sessions/${this.config.sessionID}/map-edge-attrs`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              zone: room.z,
              x: room.x,
              y: room.y,
              l: room.l,
              seam: { target_zone: seam.zone, target_tag: seam.tag, command: seam.command, order: seam.order },
              ...patch,
            }),
          },
        );
        if (!res.ok) throw new Error(await res.text());
        const data: { ok?: boolean; reason?: string; forked_to?: number } = await res.json();
        if (!data.ok) {
          if (isLatest()) this.setStatus(`Failed to save transition attribute: ${data.reason ?? "?"}`, { sticky: true });
          return false;
        }
        let status = `${room.h || "cell"} → ${seam.zone}: ${describeAttrPatch(patch, seamDirection(seam.command), this.doorOpen)}`;
        if (data.forked_to != null) status += " (map set forked to an editable copy)";
        if (isLatest()) this.setStatus(`${status} · use Undo in the cell menu to restore it`, { sticky: true });
        return true;
      } catch (err) {
        if (isLatest()) this.setStatus(`Seam attribute failed: ${err instanceof Error ? err.message : String(err)}`);
        return false;
      }
    });
  }

  private enqueueAttrWrite(
    key: string,
    write: (isLatest: () => boolean) => Promise<boolean>,
  ): Promise<boolean> {
    const generation = (this.attrWriteGeneration.get(key) ?? 0) + 1;
    this.attrWriteGeneration.set(key, generation);
    const previous = this.attrWriteTail.get(key) ?? Promise.resolve();
    const request = previous.then(() => write(
      () => !this.disposed && this.attrWriteGeneration.get(key) === generation,
    ));
    const tail = request.then(() => undefined, () => undefined);
    this.attrWriteTail.set(key, tail);
    void tail.then(() => {
      if (this.attrWriteTail.get(key) === tail) {
        this.attrWriteTail.delete(key);
        if (this.attrWriteGeneration.get(key) === generation) {
          this.attrWriteGeneration.delete(key);
        }
      }
    });
    return request;
  }

  // POST one directed mark. The endpoint is always one-way: the popup shows both
  // halves, so writing both at once would silently move a checkbox the operator
  // did not touch.
  private async writeEdgeMark(side: EdgeSide, mark: EdgeMark): Promise<void> {
    this.closeCellMenu();
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot mark the edge");
      return;
    }
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-mark-edge`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            zone: side.from.z,
            x: side.from.x,
            y: side.from.y,
            l: side.from.l,
            dir: side.dir,
            mark,
          }),
        },
      );
      if (!res.ok) throw new Error(await res.text());
      const data: { ok?: boolean; reason?: string; forked_to?: number } =
        await res.json();
      if (!data.ok) {
        this.setStatus(`Failed to mark edge: ${data.reason ?? "?"}`, {
          sticky: true,
        });
        return;
      }
      const what =
        mark === "open"
          ? "hidden passage marked"
          : mark === "blocked"
            ? "passage blocked"
            : "manual mark cleared — the room text decides the edge again" +
              " (if it says nothing about the passage, no passage will exist)";
      let status = `${side.fromLabel} → ${side.toLabel}: ${what}`;
      if (data.forked_to != null) status += " (map set forked to an editable copy)";
      this.setStatus(`${status} · use Undo in the cell menu to restore it`, {
        sticky: true,
      });
    } catch (err) {
      this.setStatus(
        `Edge mark failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // Insert the menu into the pane and place it so the WHOLE menu stays visible.
  // The menu MUST be in the DOM before measuring — its size depends on which
  // items were built (the record/undo items make it taller than before). We
  // clamp against the PANE ROOT's box, not the viewport: .map-pane is the menu's
  // containing block (CSS gives it position:relative) and the surrounding
  // .column/.panes-container use overflow:hidden, so anything outside the pane
  // would be clipped away rather than merely overlapping.
  private placeCellMenu(
    menu: HTMLElement,
    clientX: number,
    clientY: number,
  ): void {
    this.cellMenuAnchor = { clientX, clientY };
    const alreadyMounted = menu.parentElement === this.root;
    // Park it off-screen for the measuring pass so a mis-placed frame is never
    // painted at the raw click point. An existing disclosure menu stays visible:
    // visibility:hidden would blur its focused toggle during remeasurement.
    if (!alreadyMounted) menu.style.visibility = "hidden";
    menu.style.left = "0px";
    menu.style.top = "0px";
    // A disclosure may have been measured while collapsed. Remove its old cap
    // before remeasuring so expansion can claim the full pane height and scroll.
    menu.style.maxHeight = "";
    // Re-appending an already-mounted disclosure resets focus in its subtree.
    if (!alreadyMounted) this.root.appendChild(menu);
    this.cellMenu = menu;

    const bounds = this.root.getBoundingClientRect();
    const size = menu.getBoundingClientRect();
    const { left, top, maxHeight } = clampMenuPosition(
      { x: clientX - bounds.left, y: clientY - bounds.top },
      { width: size.width, height: size.height },
      { width: bounds.width, height: bounds.height },
    );
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
    // A menu taller than the pane scrolls instead of losing items off the edge.
    menu.style.maxHeight = `${maxHeight}px`;
    menu.style.visibility = "";
  }

  private onOutsideMenuClick = (e: MouseEvent): void => {
    if (this.cellMenu && !this.cellMenu.contains(e.target as Node)) {
      this.closeCellMenu();
    }
  };

  private onMenuKeydown = (e: KeyboardEvent): void => {
    if (e.key === "Escape") this.closeCellMenu();
  };

  private reconcileCellMenu(): void {
    if (this.cellMenu && this.cellMenuValid && !this.cellMenuValid()) {
      this.closeCellMenu();
    }
  }

  private closeCellMenu(): void {
    document.removeEventListener("mousedown", this.onOutsideMenuClick, true);
    document.removeEventListener("keydown", this.onMenuKeydown, true);
    this.cellMenuValid = null;
    this.cellMenuAnchor = null;
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

  // Compute a client-side route for insertion, or start the server-owned paced
  // walk to `cell`. Soft edge cases are handled inline.
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
      if (action === "send") {
        const res = await fetchWithToken(
          `/api/sessions/${this.config.sessionID}/map-walk`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              to: { zone: cell.z, x: cell.x, y: cell.y, l: cell.l },
            }),
          },
        );
        if (!res.ok) throw new Error(await res.text());
        const state: MapWalkState = await res.json();
        this.onMapWalk(state);
        if (!state.active) {
          if (!state.reason) this.setStatus("Unable to start route");
          return;
        }
        this.setStatus(`Route to "${room.h}": ${state.current + 1}/${state.total}`);
        return;
      }
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
        // A route blocked ONLY by the player's own "не водить сюда" marks is not a
        // hole in the map, and must not read like one — otherwise they go looking
        // for a passage that is right there and that they closed themselves.
        if (data.vetoed_only) {
          this.setStatus(`No route to "${room.h}": ${reason}`, {
            sticky: true,
          });
          return;
        }
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
          const fallbackSummary = `${dirs.length} ${dirs.length === 1 ? "step" : "steps"}`;
          let status = `Route to "${room.h}": ${data.summary ?? fallbackSummary} → input`;
          if (data.locked)
            status += ` · ⚠ ${data.locked} ${data.locked === 1 ? "door is" : "doors are"} marked locked — a key is required`;
          if (data.dt) status += " ⚠ death trap on path";
          this.setStatus(status, { autoHide: !data.locked && !data.dt });
        } else {
          let status = `Route to "${room.h}": ${walk}`;
          if (data.locked)
            status += ` · ⚠ ${data.locked} ${data.locked === 1 ? "door is" : "doors are"} marked locked — a key is required`;
          if (data.dt) status += " ⚠ death trap on path";
          this.setStatus(status, { autoHide: !data.locked && !data.dt });
        }
        return;
      }
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
      this.setStatus("No changes to apply");
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
        this.setStatus(`Failed to update cell: ${data.reason ?? "?"}`);
        return;
      }
      this.setStatus("Cell updated from live state", { sticky: true });
    } catch (err) {
      this.setStatus(
        `Update failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // POST the «Записать комнату» autorecord: write the room the player is
  // standing in into the map from the live room text. The backend takes the
  // coord from its own tracker (we send ours only so it can refuse a mismatch),
  // re-anchors onto the cell, and broadcasts rooms_changed — which drives our
  // reload(), turning a candidate cell into a real room. Soft failures come back
  // as 200 + {applied:false, reason}.
  private async recordRoom(cell: {
    z: string;
    x: number;
    y: number;
    l: number;
  }): Promise<void> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot record the room");
      return;
    }
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-record-room`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            zone: cell.z,
            x: cell.x,
            y: cell.y,
            l: cell.l,
          }),
        },
      );
      if (!res.ok) throw new Error(await res.text());
      const data: {
        applied?: boolean;
        created?: boolean;
        reason?: string;
        forked_to?: number;
        touches?: Array<{
          dir: string;
          hint: string;
          back_link: boolean;
        }>;
      } = await res.json();
      if (!data.applied) {
        this.setStatus(`Failed to record room: ${data.reason ?? "?"}`, {
          sticky: true,
        });
        return;
      }
      let status = data.created ? "Room recorded" : "Room updated";
      if (data.forked_to != null) status += " (map set forked to an editable copy)";
      // Report which neighbours the written exits now connect to. A one-sided
      // edge (back_link=false) exists ONLY because this record asserted it, so
      // it is the one worth eyeballing — flag those explicitly.
      const touches = data.touches ?? [];
      if (touches.length > 0) {
        const parts = touches.map(
          (t) =>
            `${t.dir}→${t.hint || "?"}${t.back_link ? "" : " (one-way)"}`,
        );
        status += ` · links: ${parts.join(", ")}`;
      }
      this.setStatus(`${status} · use Undo in the cell menu to restore it`, {
        sticky: true,
      });
    } catch (err) {
      this.setStatus(
        `Record failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // POST the undo of the last topology write on the session's active map set.
  // The journal is per-map-set (not per-pane), so this reverses whatever landed
  // last — including a write an agent made over MCP.
  private async undoMapWrite(): Promise<void> {
    if (this.config.sessionID == null) {
      this.setStatus("No session — cannot undo");
      return;
    }
    try {
      const res = await fetchWithToken(
        `/api/sessions/${this.config.sessionID}/map-undo`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: "{}",
        },
      );
      if (!res.ok) throw new Error(await res.text());
      const data: {
        applied?: boolean;
        nothing_to_undo?: boolean;
        label?: string;
        reason?: string;
        zone?: string;
        x?: number;
        y?: number;
        l?: number;
        reanchored?: boolean;
      } = await res.json();
      if (data.nothing_to_undo) {
        this.setStatus("Nothing to undo in this map set", { sticky: true });
        return;
      }
      if (!data.applied) {
        this.setStatus(`Undo was not applied: ${data.reason ?? "?"}`, {
          sticky: true,
        });
        return;
      }
      // Undo reverses whatever landed LAST on the map set — possibly an MCP
      // agent's write, not the user's — so always show which cell it touched.
      const at =
        data.zone != null
          ? ` @ ${data.zone} ${data.x},${data.y},${data.l}`
          : "";
      const re = data.reanchored ? " · position restored" : "";
      this.setStatus(
        `Undone: ${data.label ?? "last change"}${at}${re}`,
        {
          sticky: true,
        },
      );
    } catch (err) {
      this.setStatus(
        `Undo failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  // --- search ------------------------------------------------------------

  private onSearch(): void {
    this.cancelMapSearch();
    const query = this.searchInput.value.trim();
    const folded = query.toLocaleLowerCase();
    const localRooms = this.zoneCache.get(this.currentZone) || [];
    this.renderSearchResults(localRooms
      .filter((room) => folded && room.h.toLocaleLowerCase().includes(folded))
      .slice(0, 8));
    if (!query) return;

    const generation = ++this.searchGeneration;
    this.searchTimer = window.setTimeout(() => {
      this.searchTimer = null;
      void this.fetchMapSearch(query, generation);
    }, 150);
  }

  private cancelMapSearch(): void {
    if (this.searchTimer !== null) {
      window.clearTimeout(this.searchTimer);
      this.searchTimer = null;
    }
    this.searchRequest?.abort();
    this.searchRequest = null;
    this.searchGeneration++;
  }

  private async fetchMapSearch(query: string, generation: number): Promise<void> {
    const mapSetID = this.mapSetID;
    if (mapSetID == null || !this.searchPopover) return;
    const controller = new AbortController();
    this.searchRequest = controller;
    try {
      const params = new URLSearchParams({
        q: query,
        limit: "20",
        prefer_zone: this.currentZone,
      });
      const res = await fetchWithToken(
        `/api/map-sets/${mapSetID}/rooms/search?${params}`,
        { signal: controller.signal },
      );
      if (!res.ok) throw new Error(await res.text());
      const hits: MapRoomSearchResult[] = await res.json();
      if (
        this.disposed || generation !== this.searchGeneration ||
        mapSetID !== this.mapSetID || !this.searchPopover
      ) return;
      this.renderSearchResults(hits);
      if (hits.length === 0) this.setStatus(`No room matches "${query}"`);
    } catch (err) {
      if (controller.signal.aborted) return;
      this.setStatus(`Search failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      if (this.searchRequest === controller) this.searchRequest = null;
    }
  }

  private renderSearchResults(candidates: MapRoomSearchResult[]): void {
    if (!this.searchResults) return;
    this.searchResults.innerHTML = "";
    for (const candidate of candidates) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = `${candidate.h || `${candidate.x},${candidate.y},${candidate.l}`} — ${candidate.z}`;
      button.addEventListener("click", () => void this.openSearchResult(candidate));
      this.searchResults.appendChild(button);
    }
  }

  private async openSearchResult(candidate: MapRoomSearchResult): Promise<void> {
    this.closeSearchPopover();
    const rooms = await this.showZone(
      candidate.z,
      { resetView: true },
      this.nextLoadGeneration(),
    );
    if (!rooms) return;
    this.renderer.setLevel(candidate.l);
    this.buildFloorStack();
    this.renderer.centerOn(candidate.x, candidate.y, candidate.l);
    this.setStatus(`Found "${candidate.h}" in "${candidate.z}"`);
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
    if (room.se.length > 0)
      parts.push(
        `<div class="map-tip__row map-tip__seam">◈ ${
          room.se.length === 1 ? "seam" : `${room.se.length} seams`
        } (click to ${room.se.length === 1 ? "cross" : "choose"})</div>`,
      );
    this.tip.innerHTML = parts.join("");
  }

  // `sticky` marks a message that must survive the reload the action itself
  // causes: a record/undo triggers rooms_changed -> reload() -> setStatus(""),
  // which would otherwise wipe the outcome the user needs to read. See
  // nextStatusState for the exact rules.
  private setStatus(
    text: string,
    opts?: { sticky?: boolean; autoHide?: boolean },
  ): void {
    const { apply, stickyAt } = nextStatusState(
      text,
      opts?.sticky === true,
      this.stickyStatusAt,
      Date.now(),
    );
    this.stickyStatusAt = stickyAt;
    if (!apply) return;
    this.clearStatusTimer();
    this.statusText.textContent = text;
    this.statusLine.style.display = text ? "" : "none";
    if (text && opts?.autoHide) this.scheduleStatusDismissal(text);
  }

  private scheduleStatusDismissal(text: string): void {
    const timer = window.setTimeout(() => {
      // A newer status clears and replaces this handle. The identity check
      // makes an already-queued callback harmless if it arrives late.
      if (this.disposed || this.statusTimer !== timer) return;
      this.statusTimer = null;
      if (this.statusText.textContent === text) this.dismissStatus();
    }, statusAutoHideDelay(text));
    this.statusTimer = timer;
  }

  private clearStatusTimer(): void {
    if (this.statusTimer === null) return;
    window.clearTimeout(this.statusTimer);
    this.statusTimer = null;
  }

  private dismissStatus(): void {
    this.clearStatusTimer();
    // Manual dismissal is authoritative, even for a sticky write outcome.
    this.stickyStatusAt = 0;
    this.statusText.textContent = "";
    this.statusLine.style.display = "none";
  }
}

function esc(s: string): string {
  return String(s).replace(
    /[&<>"]/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c] as string,
  );
}
