// Slim room record as returned by GET /api/rooms (see go/internal/storage/mapper.go
// SlimRoom). NOTE: this schema differs from the webmap prototype's krynn_slim.json —
// in the REST feed `d` is the door-direction list, `s` is the is_dt flag, and `a`
// is the automaps seam list of "Zone|command|tag" strings.
export interface SlimRoom {
  z: string; // zone name
  t: number | null; // tag (int8, NOT unique within a zone)
  h: string; // hint (room name)
  x: number; // game X axis (N/S): N=x-1, S=x+1
  y: number; // game Y axis (W/E): E=y+1, W=y-1
  l: number; // level (floor): U=l+1, D=l-1
  e: string[]; // exit direction letters (edirs) e.g. ["N","S","U"]
  d: string[]; // door direction letters
  ch: number; // connectivity bitmask ChN..ChD
  a: string[]; // automaps seams: "targetZone|command|targetTag"
  p: boolean; // pipe (narrow corridor: no tag/hint)
  i: number | null; // imageindex (special-room glyph)
  s: boolean; // is_dt (death trap)
  dx: number;
  dy: number;
  dl: number;
  img: number; // 0|1 image present (phase 4; unused here)
}

export interface MapSet {
  id: number;
  name: string;
  zone_count: number;
  room_count: number;
  seam_count?: number;
}

// A parsed seam target from a room's `a` list entry ("zone|command|tag").
export interface SeamTarget {
  zone: string;
  command: string;
  tag: number;
}

// Live tracker position pushed via the room_position WS message.
export type Confidence = 'green' | 'yellow' | 'red';

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
}
