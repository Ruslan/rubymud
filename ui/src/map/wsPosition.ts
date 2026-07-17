import type { Confidence, PlayerPosition } from "./types";

// Shape of the mapper fields carried on a ServerMsg (see go/internal/session/
// types.go ServerMsg). All optional/omitempty on the wire.
export interface RoomPositionMessage {
  type?: string;
  confidence?: string;
  pending_moves?: number;
  position_valid?: boolean;
  position_reason?: string;
  zone?: string;
  room_x?: number;
  room_y?: number;
  room_l?: number;
  is_dt?: boolean;
  pipe?: boolean;
  room_hint?: string;
  exits_added_live?: string[];
  exits_removed_map?: string[];
}

function normalizeConfidence(c: string | undefined): Confidence {
  return c === "green" || c === "yellow" || c === "red" ? c : "red";
}

// Pure mapper from a raw room_position ServerMsg to the UI PlayerPosition model.
// Kept separate from DOM so it is unit-testable. Note Go's omitempty drops
// zero-valued coords/false flags, hence the `?? 0` / `?? false` defaults.
export function parseRoomPosition(msg: RoomPositionMessage): PlayerPosition {
  return {
    valid: msg.position_valid ?? false,
    zone: msg.zone ?? "",
    x: msg.room_x ?? 0,
    y: msg.room_y ?? 0,
    l: msg.room_l ?? 0,
    confidence: normalizeConfidence(msg.confidence),
    pendingMoves: msg.pending_moves ?? 0,
    reason: msg.position_reason || undefined,
    hint: msg.room_hint || undefined,
    isDT: msg.is_dt ?? false,
    pipe: msg.pipe ?? false,
    exitsAddedLive: msg.exits_added_live ?? [],
    exitsRemovedMap: msg.exits_removed_map ?? [],
  };
}
