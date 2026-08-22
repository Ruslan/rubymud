import { describe, expect, it } from "vitest";

import {
  attrsSummary,
  buildEdgeModel,
  emptyEdgeAttrs,
  faceAttrs,
  renderDefaultOpen,
  seamAttrs,
} from "./edgeEditor";
import type { SlimRoom } from "./types";

// The UI half of the edge-attribute behavior. The pane renders one control per
// attribute inside the per-DIRECTION block; what is decided here — and therefore
// what is tested here — is which face's attributes each block shows, and what the
// popup TELLS the player the route will do, which is the only thing they can act on.

const DEFAULT_OPEN = "откр две {dir}";

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

describe("faceAttrs", () => {
  it("fills in the defaults for a face the wire form omits", () => {
    expect(faceAttrs(room({ g: ["N"] }), "N")).toEqual(emptyEdgeAttrs);
    expect(faceAttrs(null, "N")).toEqual(emptyEdgeAttrs);
  });

  it("reads one face's attributes and ignores the others", () => {
    const r = room({
      g: ["N", "S"],
      ea: [
        { dir: "N", phrase: "двигать ящик", no_route: true },
        { dir: "S", door: true, locked: true, open_cmd: "дёрнуть рычаг" },
      ],
    });
    expect(faceAttrs(r, "N")).toEqual({
      weight: 1,
      phrase: "двигать ящик",
      door: false,
      locked: false,
      openCmd: "",
      openDelayMS: 0,
      noRoute: true,
    });
    expect(faceAttrs(r, "S")).toEqual({
      weight: 1,
      phrase: "",
      door: true,
      locked: true,
      openCmd: "дёрнуть рычаг",
      openDelayMS: 0,
      noRoute: false,
    });
  });

  it("reads a non-default directed routing weight", () => {
    expect(faceAttrs(room({ ea: [{ dir: "N", weight: 0.5 }] }), "N").weight).toBe(0.5);
  });
});

describe("seamAttrs", () => {
  it("keeps duplicate seam selectors distinct by persisted occurrence", () => {
    const r = room({
      se: [
        { o: 0, k: "seam", r: "parsed", p: true, tz: "Road", tt: 7, c: "portal", phrase: "old portal" },
        { o: 1, k: "seam", r: "parsed", p: true, tz: "Road", tt: 7, c: "portal", phrase: "new portal", weight: 2 },
      ],
    });
    expect(seamAttrs(r, {
      zone: "Road", command: "portal", tag: 7, source: "parsed", passable: true, order: 1,
    })).toMatchObject({ phrase: "new portal", weight: 2 });
  });
});

// The popup shows the two directed halves of a boundary side by side, and each
// half must carry ITS OWN attributes. Getting this wrong is not a cosmetic bug: the
// operator would edit the phrase of the direction they are not looking at.
describe("buildEdgeModel carries per-direction attributes", () => {
  it("gives each side the attributes of the face it walks", () => {
    const a = room({
      x: 0,
      y: 0,
      g: ["E"],
      ea: [{ dir: "E", phrase: "двигать ящик" }],
    });
    const b = room({
      x: 0,
      y: 1,
      g: ["W"],
      ea: [{ dir: "W", phrase: "лезть ковёр", door: true }],
    });
    const model = buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "E", a, b);
    expect(model).not.toBeNull();
    const [out, back] = model!.sides;
    expect(out.dir).toBe("E");
    expect(out.attrs.phrase).toBe("двигать ящик");
    expect(out.attrs.door).toBe(false);
    expect(back.dir).toBe("W");
    expect(back.attrs.phrase).toBe("лезть ковёр");
    expect(back.attrs.door).toBe(true);
  });

  it("offers the controls only where there is a passage to describe", () => {
    // A face with no edge at all: the backend refuses attributes on it, so the
    // popup must not present inputs that error on blur.
    const none = room({ x: 0, y: 0 });
    const m1 = buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "E", none, none);
    expect(m1!.sides[0].attrsEditable).toBe(false);
    // A hand-drawn negation is a face too, and equally has nothing to describe.
    const denied = room({ x: 0, y: 0, mb: ["E"] });
    const m2 = buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "E", denied, none);
    expect(m2!.sides[0].attrsEditable).toBe(false);
    // A derived passage does.
    const open = room({ x: 0, y: 0, g: ["E"] });
    const m3 = buildEdgeModel({ z: "Z", x: 0, y: 0, l: 0 }, "E", open, none);
    expect(m3!.sides[0].attrsEditable).toBe(true);
  });
});

// The controls show WHAT IS STORED; the summary shows WHAT THE ROUTE WILL DO.
// Those differ in exactly the cases that confuse people, so the summary is the
// thing worth pinning.
describe("attrsSummary", () => {
  it("says nothing when nothing is marked", () => {
    expect(attrsSummary(emptyEdgeAttrs, "N", DEFAULT_OPEN)).toBe("");
  });

  it("keeps a non-default weight concise", () => {
    expect(attrsSummary({ ...emptyEdgeAttrs, weight: 0.5 }, "N", DEFAULT_OPEN)).toBe("weight 0.5");
  });

  it("names the phrase the route will send instead of the direction", () => {
    const s = attrsSummary(
      { ...emptyEdgeAttrs, phrase: "двигать ящик" },
      "D",
      DEFAULT_OPEN,
    );
    expect(s).toContain("двигать ящик");
    expect(s).toContain("instead of the direction");
  });

  it("shows the DEFAULT opening command for a door with none of its own", () => {
    const s = attrsSummary({ ...emptyEdgeAttrs, door: true }, "N", DEFAULT_OPEN);
    expect(s).toContain("откр две север");
  });

  it("prefers the edge's own opening command", () => {
    const s = attrsSummary(
      { ...emptyEdgeAttrs, door: true, openCmd: "дёрнуть рычаг" },
      "N",
      DEFAULT_OPEN,
    );
    expect(s).toContain("дёрнуть рычаг");
    expect(s).not.toContain("откр две");
  });

  it("states the pause between the opening action and movement", () => {
    const s = attrsSummary(
      { ...emptyEdgeAttrs, door: true, openCmd: "повернуть рычаг", openDelayMS: 2000 },
      "D",
      DEFAULT_OPEN,
    );
    expect(s).toContain("повернуть рычаг");
    expect(s).toContain("waits 2000 ms before movement");
  });

  it("calls out an opening command with no door, which would never fire", () => {
    const s = attrsSummary(
      { ...emptyEdgeAttrs, openCmd: "дёрнуть рычаг" },
      "N",
      DEFAULT_OPEN,
    );
    expect(s).toContain("this edge has no door");
  });

  it("says a veto still lets you walk — that is what separates it from a negation", () => {
    const s = attrsSummary({ ...emptyEdgeAttrs, noRoute: true }, "N", DEFAULT_OPEN);
    expect(s).toContain("routes avoid this edge");
    expect(s).toContain("manual movement still works");
  });

  it("reports a lock as needing a key rather than as a wall", () => {
    const s = attrsSummary(
      { ...emptyEdgeAttrs, door: true, locked: true },
      "N",
      DEFAULT_OPEN,
    );
    expect(s).toContain("a key is required");
  });
});

describe("renderDefaultOpen", () => {
  it("substitutes the russian direction word", () => {
    expect(renderDefaultOpen(DEFAULT_OPEN, "W")).toBe("откр две запад");
    expect(renderDefaultOpen(DEFAULT_OPEN, "D")).toBe("откр две вниз");
  });

  it("drops the argument for a direction it cannot name, instead of leaving {dir}", () => {
    expect(renderDefaultOpen(DEFAULT_OPEN, "?")).toBe("откр две");
  });
});

// --- the door-opening command belongs to the MUD, not to the client ---------
//
// It was a constant in shared Go code for one commit, which put "откр две north"
// into an English mud's walk. It now comes from the session's ROOM-FORMAT PROFILE
// and reaches the pane over the wire, so the pane's job is to render whatever it
// was told — including nothing.

describe("renderDefaultOpen", () => {
  it("substitutes the russian direction word for {dir}", () => {
    expect(renderDefaultOpen("откр две {dir}", "W")).toBe("откр две запад");
    expect(renderDefaultOpen("откр две {dir}", "D")).toBe("откр две вниз");
  });

  it("substitutes the canonical english letter for {dir_en}", () => {
    expect(renderDefaultOpen("open door {dir_en}", "E")).toBe("open door e");
  });

  it("leaves a template with no placeholder alone", () => {
    expect(renderDefaultOpen("open door", "N")).toBe("open door");
  });

  it("drops the argument for a direction it cannot name, instead of leaving {dir}", () => {
    expect(renderDefaultOpen("откр две {dir}", "?")).toBe("откр две");
  });

  it("stays EMPTY for a format that describes no door verb", () => {
    // The whole point: "" is not "open with nothing", it is "do not claim a command".
    expect(renderDefaultOpen("", "N")).toBe("");
  });
});

describe("attrsSummary without a room-format door verb", () => {
  it("says the route will NOT open the door, rather than promising an empty command", () => {
    const s = attrsSummary({ ...emptyEdgeAttrs, door: true }, "N", "");
    expect(s).not.toContain("«»");
    expect(s).toContain("the route will not open the door");
  });

  it("still promises the edge's OWN command, which works on any mud", () => {
    const s = attrsSummary(
      { ...emptyEdgeAttrs, door: true, openCmd: "pull lever" },
      "N",
      "",
    );
    expect(s).toContain("pull lever");
  });
});

// Go's renderDoorTemplate uses strings.ReplaceAll and these two are declared
// mirrors, so a template naming a placeholder twice must render the same on both
// sides — otherwise the pane promises one command and the route sends another.
describe("renderDefaultOpen mirrors Go's ReplaceAll", () => {
  it("substitutes EVERY occurrence, not just the first", () => {
    expect(renderDefaultOpen("откр {dir} две {dir}", "N")).toBe(
      "откр север две север",
    );
    expect(renderDefaultOpen("open {dir_en} door {dir_en}", "E")).toBe(
      "open e door e",
    );
  });
});
