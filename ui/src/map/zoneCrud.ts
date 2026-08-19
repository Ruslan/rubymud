import { fetchWithToken } from "../dom";

export interface ZoneCoord {
  x: number;
  y: number;
  l: number;
}

export interface ZoneMovePreview {
  revision: number | null;
  rooms: number;
  internal_edges: number;
  boundary_grid_edges: number;
  portals: number;
  incoming: number;
  outgoing: number;
  cross_zone_contacts: number;
  components: number;
  disconnected: boolean;
  annotations_moved: number;
  inhabitants_moved?: number;
  collision_count: number;
  collisions?: ZoneCoord[];
  collisions_truncated?: boolean;
  source_empties: boolean;
  target_exists: boolean;
}

export interface ZoneMoveResponse {
  ok: boolean;
  reason?: string;
  active_map_set?: number | null;
  forked_to?: number | null;
  preview?: ZoneMovePreview | null;
}

export function canonicalZoneName(raw: string): { value: string; error: string | null } {
  const value = raw.trim().normalize("NFC");
  if (!value) return { value, error: "Zone name is required" };
  if ([...value].length > 256) return { value, error: "Zone name exceeds 256 characters" };
  if (/\p{Cc}/u.test(value)) return { value, error: "Zone name contains a control character" };
  return { value, error: null };
}

export function canonicalCoords(coords: Iterable<ZoneCoord>): ZoneCoord[] {
  const unique = new Map<string, ZoneCoord>();
  for (const coord of coords) {
    if (![coord.x, coord.y, coord.l].every(Number.isSafeInteger)) throw new Error("Room coordinates must be safe integers");
    const canonical = {
      x: Object.is(coord.x, -0) ? 0 : coord.x,
      y: Object.is(coord.y, -0) ? 0 : coord.y,
      l: Object.is(coord.l, -0) ? 0 : coord.l,
    };
    unique.set(`${canonical.x},${canonical.y},${canonical.l}`, canonical);
  }
  return [...unique.values()].sort((a, b) => a.x - b.x || a.y - b.y || a.l - b.l);
}

export function previewSummary(preview: ZoneMovePreview): string {
  const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;
  const parts = [
    plural(preview.rooms, "room"),
    plural(preview.components, "connected component"),
    plural(preview.internal_edges, "internal grid edge"),
    plural(preview.boundary_grid_edges, "boundary grid edge"),
    plural(preview.portals, "portal"),
    `${preview.incoming} incoming / ${preview.outgoing} outgoing`,
    plural(preview.cross_zone_contacts, "cross-zone contact"),
    plural(preview.annotations_moved, "annotation"),
    ...(preview.inhabitants_moved
      ? [plural(preview.inhabitants_moved, "inhabitant placement")]
      : []),
    preview.target_exists ? "existing target" : "new target",
  ];
  if (preview.collision_count) parts.push(`${plural(preview.collision_count, "collision")} (apply blocked)`);
  if (preview.disconnected) parts.push("warning: disconnected result");
  return parts.join(" · ");
}

export async function postZoneMove(
  sessionID: number,
  path: "map-zones/preview" | "map-zones/apply" | "map-zones/rename/preview" | "map-zones/rename/apply",
  body: Record<string, unknown>,
  signal?: AbortSignal,
): Promise<ZoneMoveResponse> {
  const res = await fetchWithToken(`/api/sessions/${sessionID}/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  const text = await res.text();
  let data: ZoneMoveResponse;
  try {
    data = JSON.parse(text) as ZoneMoveResponse;
  } catch {
    throw new Error(text || `HTTP ${res.status}`);
  }
  if (!res.ok) throw new Error(data.reason ?? `HTTP ${res.status}`);
  return data;
}
