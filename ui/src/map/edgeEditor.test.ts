import { describe, expect, it } from "vitest";

import {
  buildEdgeModel,
  canClear,
  faceState,
  markFor,
  passableNow,
  provenanceLabel,
} from "./edgeEditor";
import type { SlimRoom } from "./types";

function room(over: Partial<SlimRoom>): SlimRoom {
  return {
    z: "Z",
    t: null,
    h: "",
    x: 0,
    y: 0,
    l: 0,
    e: [],
    d: [],
    ch: 0,
    se: [],
    p: false,
    i: null,
    s: false,
    dx: 0,
    dy: 0,
    dl: 0,
    img: 0,
    m: [],
    mb: [],
    g: [],
    gf: [],
    ...over,
  };
}

describe("edge face state", () => {
  it("reads the graph, not the exits line", () => {
    // The room PRINTS an exit south and the player has denied it: the exits line
    // and the graph disagree, and the editor must follow the graph.
    const denied = room({ e: ["S"], mb: ["S"] });
    expect(faceState(denied, "S")).toBe("blocked");
    expect(passableNow(faceState(denied, "S"))).toBe(false);

    // The room prints NOTHING and the player marked a way east.
    const secret = room({ e: [], m: ["E"] });
    expect(faceState(secret, "E")).toBe("open");
    expect(passableNow(faceState(secret, "E"))).toBe(true);

    const parsed = room({ e: ["N"], g: ["N"] });
    expect(faceState(parsed, "N")).toBe("derived");
    expect(faceState(parsed, "W")).toBe("none");
    expect(faceState(null, "N")).toBe("none");
  });

  it("tells a grid-fallback edge apart from a parsed one", () => {
    // Nothing in EITHER room's text claims this passage — it exists because the
    // two cells are adjacent and neither denies it. Calling that "разобран из
    // текста комнаты" would be a lie, and it is the likeliest edge to be denied.
    const fb = room({ e: [], gf: ["N"] });
    expect(faceState(fb, "N")).toBe("fallback");
    expect(passableNow(faceState(fb, "N"))).toBe(true);
    expect(provenanceLabel("fallback")).not.toBe(provenanceLabel("derived"));
    expect(provenanceLabel("fallback")).toContain("смежности");
    // It is deniable and un-deniable exactly like a parsed edge.
    expect(markFor("fallback", false)).toBe("blocked");
    expect(markFor("fallback", true)).toBeNull();
    expect(canClear("fallback")).toBe(false);
  });

  it("names the provenance of every state distinctly", () => {
    const seen = new Set(
      (["open", "blocked", "derived", "fallback", "none"] as const).map(
        provenanceLabel,
      ),
    );
    expect(seen.size).toBe(5);
    // The two hand-drawn states must be recognisable as hand-drawn.
    expect(provenanceLabel("open")).toContain("вручную");
    expect(provenanceLabel("blocked")).toContain("вручную");
    expect(provenanceLabel("derived")).toContain("текста");
  });
});

describe("what a toggle writes", () => {
  it("denies what the MAP claims", () => {
    expect(markFor("derived", false)).toBe("blocked");
    expect(markFor("fallback", false)).toBe("blocked");
  });

  it("withdraws YOUR OWN mark as 'auto', symmetrically in both directions", () => {
    // Un-denying must not assert a secret passage the player never claimed, and
    // un-drawing a hidden exit must not assert that the passage is impossible —
    // which is what it used to do, leaving the popup describing the face as
    // "отрицён вручную (текст комнаты о нём говорит)" when the text says nothing.
    expect(markFor("blocked", true)).toBe("auto");
    expect(markFor("open", false)).toBe("auto");
  });

  it("marks a hidden exit where there is no edge at all", () => {
    expect(markFor("none", true)).toBe("open");
  });

  it("writes nothing when the toggle asks for the state already in place", () => {
    // Otherwise a stray click would push a no-op onto the undo stack, and the
    // next «Отменить» would appear to do nothing.
    expect(markFor("derived", true)).toBeNull();
    expect(markFor("open", true)).toBeNull();
    expect(markFor("blocked", false)).toBeNull();
    expect(markFor("none", false)).toBeNull();
  });

  it("offers 'clear' only where a hand-drawn row decides the face", () => {
    expect(canClear("open")).toBe(true);
    expect(canClear("blocked")).toBe(true);
    expect(canClear("derived")).toBe(false);
    expect(canClear("none")).toBe(false);
  });
});

describe("edge model", () => {
  it("shows BOTH directed halves of one boundary, independently", () => {
    // a --S--> b is hand-drawn open; b --N--> a is derived. One boundary, two
    // different facts: the popup has to show both or a one-way passage is
    // invisible.
    const a = room({ x: 0, y: 0, h: "Проход", m: ["S"] });
    const b = room({ x: 1, y: 0, h: "Ящики", e: ["N"], g: ["N"] });
    const model = buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "S", a, b);
    expect(model).not.toBeNull();
    const [out, back] = model!.sides;

    expect(out.dir).toBe("S");
    expect(out.fromLabel).toBe("Проход");
    expect(out.toLabel).toBe("Ящики");
    expect(out.state).toBe("open");
    expect(out.passable).toBe(true);

    expect(back.dir).toBe("N");
    expect(back.fromLabel).toBe("Ящики");
    expect(back.toLabel).toBe("Проход");
    expect(back.state).toBe("derived");
    expect(back.passable).toBe(true);

    // The neighbour coord follows the step deltas (S => x+1).
    expect(model!.b).toEqual({ z: "Z", x: 1, y: 0, l: 0 });
  });

  it("labels a nameless cell by its coord rather than an empty string", () => {
    const a = room({ x: 2, y: 3, h: "" });
    const model = buildEdgeModel({ z: "Z", x: 2, y: 3, l: 0 }, "E", a, null);
    expect(model!.sides[0].fromLabel).toBe("2,3,0");
    expect(model!.sides[0].toLabel).toBe("2,4,0");
  });

  it("covers the vertical directions too (they have no boundary on screen but the model does)", () => {
    const a = room({ m: ["U"] });
    const model = buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "U", a, null);
    expect(model!.sides[0].state).toBe("open");
    expect(model!.sides[1].dir).toBe("D");
    expect(model!.b.l).toBe(1);
  });

  it("returns null for a direction that is not a single grid step", () => {
    expect(buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "NE", null, null)).toBeNull();
  });
});
