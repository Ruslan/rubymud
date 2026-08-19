import { afterEach, describe, expect, it, vi } from "vitest";
import { canonicalCoords, postZoneMove, previewSummary } from "./zoneCrud";

afterEach(() => vi.unstubAllGlobals());

describe("zone CRUD request shape", () => {
  it("canonicalizes exact selected coordinates without a world payload", () => {
    expect(canonicalCoords([
      { x: 2, y: 1, l: 0 }, { x: 0, y: 3, l: 1 }, { x: 2, y: 1, l: 0 },
    ])).toEqual([
      { x: 0, y: 3, l: 1 }, { x: 2, y: 1, l: 0 },
    ]);
  });

  it("keeps preview feedback bounded to server aggregates", () => {
    expect(previewSummary({
      revision: 4, rooms: 3, internal_edges: 0, boundary_grid_edges: 0,
      portals: 0, incoming: 0, outgoing: 0, cross_zone_contacts: 0,
      components: 2, disconnected: true, annotations_moved: 0,
      inhabitants_moved: 2,
      collision_count: 1, source_empties: false, target_exists: true,
    })).toBe("3 rooms · 2 connected components · 0 internal grid edges · 0 boundary grid edges · 0 portals · 0 incoming / 0 outgoing · 0 cross-zone contacts · 0 annotations · 2 inhabitant placements · existing target · 1 collision (apply blocked) · warning: disconnected result");
  });

  it("normalizes integer zero and rejects coordinates JSON cannot represent exactly", () => {
    expect(canonicalCoords([{ x: -0, y: 0, l: -0 }])).toEqual([{ x: 0, y: 0, l: 0 }]);
    expect(() => canonicalCoords([{ x: 0.5, y: 0, l: 0 }])).toThrow("safe integers");
  });

  it("preserves a non-JSON server error instead of reading the response body twice", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("upstream unavailable", { status: 502 })));
    await expect(postZoneMove(7, "map-zones/preview", {})).rejects.toThrow("upstream unavailable");
  });
});
