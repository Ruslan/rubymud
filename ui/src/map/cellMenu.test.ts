import { describe, expect, it } from "vitest";

import {
  canRoute,
  cellPatchPayload,
  formatExitDiff,
  hasExitDiff,
  isCurrentCell,
  statusHeaderText,
  walkUpToFirstDoor,
} from "./cellMenu";
import type { PlayerPosition } from "./types";

function pos(over: Partial<PlayerPosition> = {}): PlayerPosition {
  return {
    valid: true,
    zone: "Z",
    x: 0,
    y: 0,
    l: 0,
    confidence: "green",
    pendingMoves: 0,
    exitsAddedLive: [],
    exitsRemovedMap: [],
    ...over,
  };
}

describe("statusHeaderText", () => {
  it("shows the glyph + confidence, with reason when present", () => {
    expect(statusHeaderText(pos({ confidence: "green" }))).toBe("🟢 green");
    expect(
      statusHeaderText(pos({ confidence: "yellow", reason: "assumed cell" })),
    ).toBe("🟡 yellow · assumed cell");
    expect(statusHeaderText(pos({ confidence: "red", reason: "lost" }))).toBe(
      "🔴 red · lost",
    );
  });

  it("handles a null position", () => {
    expect(statusHeaderText(null)).toBe("⚪ нет позиции");
  });
});

describe("hasExitDiff", () => {
  it("is true only when yellow AND a diff side is non-empty", () => {
    expect(
      hasExitDiff(pos({ confidence: "yellow", exitsAddedLive: ["U"] })),
    ).toBe(true);
    expect(
      hasExitDiff(pos({ confidence: "yellow", exitsRemovedMap: ["N"] })),
    ).toBe(true);
    // Yellow but no diff.
    expect(hasExitDiff(pos({ confidence: "yellow" }))).toBe(false);
    // Green with a diff (shouldn't happen, but gate on yellow).
    expect(
      hasExitDiff(pos({ confidence: "green", exitsAddedLive: ["U"] })),
    ).toBe(false);
    expect(hasExitDiff(null)).toBe(false);
  });
});

describe("formatExitDiff", () => {
  it("formats added + removed", () => {
    expect(
      formatExitDiff(pos({ exitsAddedLive: ["U"], exitsRemovedMap: ["N"] })),
    ).toBe("выходы: +U live / −N map");
  });
  it("formats added only", () => {
    expect(formatExitDiff(pos({ exitsAddedLive: ["U", "D"] }))).toBe(
      "выходы: +U+D live",
    );
  });
  it("formats removed only", () => {
    expect(formatExitDiff(pos({ exitsRemovedMap: ["N", "S"] }))).toBe(
      "выходы: −N−S map",
    );
  });
  it("is empty with no diff", () => {
    expect(formatExitDiff(pos())).toBe("");
    expect(formatExitDiff(null)).toBe("");
  });
});

describe("canRoute", () => {
  it("requires a valid, non-red position", () => {
    expect(canRoute(pos({ confidence: "green" }))).toBe(true);
    expect(canRoute(pos({ confidence: "yellow" }))).toBe(true);
    expect(canRoute(pos({ confidence: "red" }))).toBe(false);
    expect(canRoute(pos({ valid: false }))).toBe(false);
    expect(canRoute(null)).toBe(false);
  });
});

describe("isCurrentCell", () => {
  it("matches the player's exact cell", () => {
    const p = pos({ zone: "Z", x: 2, y: 3, l: 1 });
    expect(isCurrentCell(p, { z: "Z", x: 2, y: 3, l: 1 })).toBe(true);
    expect(isCurrentCell(p, { z: "Z", x: 2, y: 3, l: 0 })).toBe(false);
    expect(isCurrentCell(p, { z: "Other", x: 2, y: 3, l: 1 })).toBe(false);
    expect(isCurrentCell(null, { z: "Z", x: 2, y: 3, l: 1 })).toBe(false);
  });
});

describe("cellPatchPayload", () => {
  it("maps the tracker diff into add/remove exits", () => {
    expect(
      cellPatchPayload(pos({ exitsAddedLive: ["U"], exitsRemovedMap: ["N"] })),
    ).toEqual({ add_exits: ["U"], remove_exits: ["N"] });
    expect(cellPatchPayload(null)).toEqual({ add_exits: [], remove_exits: [] });
  });
});

describe("walkUpToFirstDoor", () => {
  it("returns the full walk when there is no door", () => {
    expect(walkUpToFirstDoor(["w", "e", "s"], -1)).toEqual({
      walk: ["w", "e", "s"],
      truncatedAtDoor: false,
    });
  });
  it("truncates before the first door hop", () => {
    expect(walkUpToFirstDoor(["w", "e", "s", "n"], 2)).toEqual({
      walk: ["w", "e"],
      truncatedAtDoor: true,
    });
  });
  it("yields an empty walk when the first hop is a door", () => {
    expect(walkUpToFirstDoor(["w", "e"], 0)).toEqual({
      walk: [],
      truncatedAtDoor: true,
    });
  });
});
