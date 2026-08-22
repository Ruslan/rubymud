// Slim room record as returned by GET /api/rooms (see storage.SlimRoom).
export interface SlimRoom {
  z: string; // zone name
  t: number | null; // tag (int8, NOT unique within a zone)
  h: string; // hint (room name)
  x: number; // game X axis (N/S): N=x-1, S=x+1
  y: number; // game Y axis (W/E): E=y+1, W=y-1
  l: number; // level (floor): U=l+1, D=l-1
  e: string[]; // exit direction letters (edirs) e.g. ["N","S","U"]
  d: string[]; // door direction letters
  me: string[]; // directions explicitly allowed to appear/disappear in this room
  ch: number; // connectivity bitmask ChN..ChD
  p: boolean; // pipe (narrow corridor: no tag/hint)
  i: number | null; // imageindex (special-room glyph)
  s: boolean; // is_dt (death trap)
  dx: number;
  dy: number;
  dl: number;
  px?: number; // sparse presentation x; logical x remains identity
  py?: number;
  pl?: number;
  pin?: boolean;
  img: number; // 0|1 image present (unused here)
  // The MATERIALIZED topology of this cell, per outgoing direction. `e` above is
  // the exits LINE the MUD printed; these are what the graph actually holds, and
  // the two are deliberately allowed to disagree — that disagreement IS the
  // hand-drawn layer.
  // The ATTRIBUTE layer, one entry per outgoing face that carries something (see
  // storage.SlimRoom.EA). Absent = every default: no phrase, no door, routable.
  // Only the interesting faces travel, so this is a list, not six slots per room.
  ea?: SlimEdgeAttr[];
  se: SlimSeam[]; // authoritative outgoing seams in source occurrence order
  // Outgoing grid edges that land in another zone. These remain grid topology;
  // the sparse marker lets the zone-local renderer show the boundary.
  xz?: SlimCrossZoneGrid[];
  co?: SlimCorridor[]; // exact directional corridors; one command, no phantom cells
  m: string[]; // hand-drawn hidden exits ("there is a way, whatever the line says")
  mb: string[]; // hand-drawn negations   ("there is NO way, whatever the line says")
  g: string[]; // rows PARSED from the room's own exit data
  gf: string[]; // rows that exist only because BOTH ends carry no exit data at all
  // (the permissive grid fallback) — nothing in either room's text says there is a
  // way, so this is a different statement from `g` and the edge editor says so.
}

export interface SlimCorridor {
  dir: string; r: string; p: boolean;
  z: string; x: number; y: number; l: number;
  c: string; weight?: number; phrase?: string; door?: boolean; locked?: boolean;
  open_cmd?: string; no_route?: boolean;
}

export interface MapRoomSearchResult {
  z: string;
  h: string;
  desc?: string;
  x: number;
  y: number;
  l: number;
}

export interface SlimCrossZoneGrid {
  dir: string;
  r: string;
  p: boolean;
  z: string;
  x: number;
  y: number;
  l: number;
}

// One directed face's hand-drawn attributes: how the passage is taken, and whether
// the router may use it. Every field is per-DIRECTION — "двигать ящик" takes you
// down and does not bring you back, a door locked one way opens freely the other.
export interface SlimEdgeAttr {
  dir: string;
  weight?: number; // routing cost; omitted is the default 1
  phrase?: string; // the command that takes the passage instead of the direction
  door?: boolean; // a door stands on this face
  locked?: boolean; // that door is locked by default from this side
  open_cmd?: string; // the command that opens it from this side
  no_route?: boolean; // walkable, but the router must not plan through it
}

export interface SlimSeam {
  o: number;
  k: string;
  r: string;
  p: boolean;
  tz: string;
  tt?: number;
  z?: string;
  x?: number;
  y?: number;
  l?: number;
  c: string;
  weight?: number;
  phrase?: string;
  door?: boolean;
  locked?: boolean;
  open_cmd?: string;
  no_route?: boolean;
}

export interface MapSet {
  id: number;
  name: string;
  zone_count: number;
  room_count: number;
  seam_count?: number;
}

// A normalized, authoritative seam target from the materialized edge list.
export interface SeamTarget {
  zone: string;
  command: string;
  tag: number | null;
  source: string;
  passable: boolean;
  order: number;
  to?: { zone: string; x: number; y: number; l: number };
}

// Live tracker position pushed via the room_position WS message.
export type Confidence = "green" | "yellow" | "red";

export interface PlayerPosition {
  valid: boolean;
  zone: string;
  x: number;
  y: number;
  l: number;
  confidence: Confidence;
  pendingMoves: number;
  reason?: string;
  hint?: string;
  isDT?: boolean;
  pipe?: boolean;
  // Exit diff surfaced by the tracker on a yellow (assumed-cell) mismatch:
  // exitsAddedLive = seen in the game but missing from the map; exitsRemovedMap =
  // on the map but not observed live. Drives the cell menu's "update from live
  // state" action.
  exitsAddedLive?: string[];
  exitsRemovedMap?: string[];
  // Mirrors mapper.Position.EventDescribesCell: whether the live room block
  // behind this position actually describes the cell at (x,y,l). False while the
  // tracker merely HOLDS a last-known coord (ambiguous match, hint-less event,
  // text veto, true loss) — recording there would overwrite an unrelated room.
  eventDescribesCell?: boolean;
}

// The live room block the parser last saw, pushed via the `room` WS message
// (room_hint/room_desc/room_exits). It describes where the player IS right now,
// which is what the «Записать комнату» autorecord writes into the map — so the
// record action is only offered while this is present.
export interface LiveRoom {
  hint: string;
  desc: string;
  exits: string;
}

export interface RoomScan {
  mapSetID: number;
  recordingRevision: number;
  zone: string;
  x: number;
  y: number;
  l: number;
  hints: Array<{ dir: string; hint: string }>;
}

export interface MapRecordingState {
  mode: "off" | "armed" | "recording" | "conflict";
  reason?: string;
  reason_code?: string;
  reason_args?: Record<string, unknown>;
  reason_detail?: string;
  pending: number;
  revision: number;
  conflict?: {
    id: number;
    move: { command: string; direction: string; source: string; from: { zone: string; x: number; y: number; l: number }; expected: { zone: string; x: number; y: number; l: number } };
    expected: { zone: string; x: number; y: number; l: number };
    expected_hint: string;
    expected_room: { hint: string; desc: string; exits: string };
    observed: { hint: string; desc: string; exits: string };
    reason: string;
    reason_code?: string;
    reason_args?: Record<string, unknown>;
    reason_detail?: string;
    mutable_exit_candidates?: string[];
    expected_mutable_exits?: string[];
  };
}
