import { fetchWithToken } from "../dom";
import { mapperI18n, type MapperI18n } from './i18n';

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

export function canonicalZoneName(raw: string, i18n: MapperI18n = mapperI18n): { value: string; error: string | null } {
  const value = raw.trim().normalize("NFC");
  if (!value) return { value, error: i18n.t('zoneNameRequired') };
  if ([...value].length > 256) return { value, error: i18n.t('zoneNameLong') };
  if (/\p{Cc}/u.test(value)) return { value, error: i18n.t('zoneNameControl') };
  return { value, error: null };
}

export function canonicalCoords(coords: Iterable<ZoneCoord>, i18n: MapperI18n = mapperI18n): ZoneCoord[] {
  const unique = new Map<string, ZoneCoord>();
  for (const coord of coords) {
    if (![coord.x, coord.y, coord.l].every(Number.isSafeInteger)) throw new Error(i18n.t('safeCoords'));
    const canonical = {
      x: Object.is(coord.x, -0) ? 0 : coord.x,
      y: Object.is(coord.y, -0) ? 0 : coord.y,
      l: Object.is(coord.l, -0) ? 0 : coord.l,
    };
    unique.set(`${canonical.x},${canonical.y},${canonical.l}`, canonical);
  }
  return [...unique.values()].sort((a, b) => a.x - b.x || a.y - b.y || a.l - b.l);
}

export function previewSummary(preview: ZoneMovePreview, i18n: MapperI18n = mapperI18n): string {
  const parts = [
    i18n.plural('room', preview.rooms),
    i18n.plural('component', preview.components),
    i18n.plural('internalEdge', preview.internal_edges),
    i18n.plural('boundaryEdge', preview.boundary_grid_edges),
    i18n.plural('portal', preview.portals),
    i18n.t('inOut', { incoming: preview.incoming, outgoing: preview.outgoing }),
    i18n.plural('contact', preview.cross_zone_contacts),
    i18n.plural('annotation', preview.annotations_moved),
    ...(preview.inhabitants_moved
      ? [i18n.plural('inhabitant', preview.inhabitants_moved)]
      : []),
    preview.target_exists ? i18n.t('existingTarget') : i18n.t('newTarget'),
  ];
  if (preview.collision_count) parts.push(i18n.t('collisionsBlocked', { collisions: i18n.plural('collision', preview.collision_count) }));
  if (preview.disconnected) parts.push(i18n.t('disconnected'));
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
