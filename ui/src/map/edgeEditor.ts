import { stepDelta } from "./geometry";
import type { SeamTarget, SlimEdgeAttr, SlimRoom, SlimSeam } from "./types";

// The EDGE EDITOR's pure model: everything the popup on a cell boundary needs,
// with no DOM. pane.ts renders it; this file decides what it says and what a
// click writes.
//
// WHY A POPUP ON THE BOUNDARY AND NOT ITEMS IN THE CELL MENU. Edges are DIRECTED
// (one stored row = one traversal), so a boundary between two cells is TWO
// independent facts: "К1 → К2" and "К2 → К1". A cell menu can only ever talk
// about the cell it was opened on, which hides half of that and makes a one-way
// passage — or a one-way wall — unexpressible in the UI even though the storage
// supports it. The popup shows both halves side by side.

// A cell coordinate in game space.
export interface Cell {
  z: string;
  x: number;
  y: number;
  l: number;
}

// FaceState is what the graph currently holds for ONE directed face.
//   "open"     — a hand-drawn hidden exit (source=manual, passable)
//   "blocked"  — a hand-drawn negation    (source=manual, not passable)
//   "derived"  — an edge the room's own exits produced
//   "fallback" — an edge that exists ONLY because neither room says anything about
//                its exits, so bare grid adjacency connects them. Not the same
//                statement as "derived", and worth its own label: nothing in the
//                text claims this passage, which makes it the likeliest thing a
//                player will want to deny.
//   "none"     — no edge at all
export type FaceState = "open" | "blocked" | "derived" | "fallback" | "none";

// The mark a write asks for, matching the backend's `mark` argument.
export type EdgeMark = "open" | "blocked" | "auto";

export function faceState(room: SlimRoom | null, dir: string): FaceState {
  if (!room) return "none";
  if (room.m.includes(dir)) return "open";
  if (room.mb.includes(dir)) return "blocked";
  if (room.g.includes(dir)) return "derived";
  if (room.gf.includes(dir)) return "fallback";
  return "none";
}

// passableNow: does the graph let you walk this face right now? This is the
// checkbox's checked state, and it is deliberately NOT "the room lists this exit":
// a negation leaves the exit in the room's printed exits and takes it out of the
// graph, which is the entire point of the layer.
export function passableNow(state: FaceState): boolean {
  return state === "open" || state === "derived" || state === "fallback";
}

// provenanceLabel explains WHERE the current state came from. The player has to
// be able to tell "the MUD said so" from "I marked it", otherwise they cannot
// tell their own experiment from the map.
export function provenanceLabel(state: FaceState): string {
  switch (state) {
    case "open":
      return "passage marked manually (not present in the room text)";
    case "blocked":
      return "passage blocked manually (the room text lists it)";
    case "derived":
      return "passage parsed from the room text";
    case "fallback":
      return "adjacency-only passage: neither room provides exit data";
    default:
      return "no passage";
  }
}

// markFor decides what a toggle WRITES, given where the face is now.
//
// The rule is symmetric, and the symmetry is the point: UNCHECKING YOUR OWN MARK
// TAKES IT BACK; unchecking something the MAP claims contradicts the map.
//
//   from "open" (your own hidden exit), want=false   -> "auto"
//   from "blocked" (your own negation), want=true    -> "auto"
//        Both are "I withdraw what I said" — drop the hand-drawn row and let the
//        room's own exits decide the face again. Withdrawing a hidden exit must NOT
//        write a negation: the player never claimed the passage is impossible, only
//        that they had drawn one, and the popup would then have shown them "проход
//        отрицён вручную (текст комнаты о нём говорит)" about a face whose text says
//        nothing. Same in the other direction: withdrawing a negation must not
//        assert a secret passage.
//        If the text does describe the passage it comes straight back as "derived";
//        if not, the face lands on "none" and a second toggle states the new thing
//        deliberately. Each step is a separate, separately undoable statement.
//   from "derived"/"fallback" (the map's claim), want=false -> "blocked"
//        Contradicting the map is exactly what a negation is for, and a hand-drawn
//        row is the only thing that survives the next re-record.
//   from "none", want=true -> "open": a hidden exit.
//
// Returns null when the toggle asks for the state the face is already in (nothing
// to write) — the caller skips the request instead of pushing a no-op onto the
// undo stack.
export function markFor(state: FaceState, want: boolean): EdgeMark | null {
  if (want === passableNow(state)) return null;
  // Withdrawing one's own mark, in either direction.
  if (state === "open" || state === "blocked") return "auto";
  return want ? "open" : "blocked";
}

// clearMark is the "let the room's exits decide this face" action, offered only
// when a hand-drawn row is what is standing in the way.
export function canClear(state: FaceState): boolean {
  return state === "open" || state === "blocked";
}

// neighbourCell returns the grid cell one step from `cell` in dir, or null for a
// direction that is not a single grid step.
export function neighbourCell(cell: Cell, dir: string): Cell | null {
  const d = stepDelta(dir);
  if (!d) return null;
  return { z: cell.z, x: cell.x + d.dx, y: cell.y + d.dy, l: cell.l + d.dl };
}

export const OPPOSITE: Record<string, string> = {
  N: "S",
  S: "N",
  E: "W",
  W: "E",
  U: "D",
  D: "U",
};

// --- the attribute layer ----------------------------------------------------

// EdgeAttrs is one face's attribute set, with the defaults filled in — the shape
// the popup's controls bind to. `SlimEdgeAttr` on the wire omits its defaults;
// this is that record made total, so the renderer never branches on undefined.
export interface EdgeAttrs {
  weight: number;
  phrase: string;
  door: boolean;
  locked: boolean;
  openCmd: string;
  noRoute: boolean;
}

export const emptyEdgeAttrs: EdgeAttrs = {
  weight: 1,
  phrase: "",
  door: false,
  locked: false,
  openCmd: "",
  noRoute: false,
};

// faceAttrs reads one face's attributes off the room's `ea` list.
export function faceAttrs(room: SlimRoom | null, dir: string): EdgeAttrs {
  const hit: SlimEdgeAttr | undefined = room?.ea?.find((a) => a.dir === dir);
  if (!hit) return { ...emptyEdgeAttrs };
  return {
    weight: hit.weight ?? 1,
    phrase: hit.phrase ?? "",
    door: hit.door === true,
    locked: hit.locked === true,
    openCmd: hit.open_cmd ?? "",
    noRoute: hit.no_route === true,
  };
}

// seamAttrs reads one exact persisted occurrence, so duplicate legacy selectors
// retain their own attribute payloads in the editor.
export function seamAttrs(room: SlimRoom, seam: SeamTarget): EdgeAttrs {
  const hit: SlimSeam | undefined = room.se?.find((edge) => edge.o === seam.order);
  if (!hit) return { ...emptyEdgeAttrs };
  return {
    weight: hit.weight ?? 1,
    phrase: hit.phrase ?? "",
    door: hit.door === true,
    locked: hit.locked === true,
    openCmd: hit.open_cmd ?? "",
    noRoute: hit.no_route === true,
  };
}

// attrsSummary is the one-line human reading of a face's attributes, shown under
// the controls. It exists because the controls alone do not say what the ROUTE will
// do, and that is the only thing the player actually cares about.
// `defaultOpen` is what the router falls back to when no per-edge command is
// recorded — the pane passes the same template the backend uses.
export function attrsSummary(
  attrs: EdgeAttrs,
  dir: string,
  defaultOpen: string,
): string {
  const parts: string[] = [];
  if (attrs.weight !== 1) parts.push(`weight ${attrs.weight}`);
  if (attrs.phrase) {
    parts.push(`route sends '${attrs.phrase}' instead of the direction`);
  }
  if (attrs.door) {
    // `defaultOpen` is the SESSION's room-format template, and it is empty for every
    // format but RMUD's. Empty means the route sends nothing, and saying «маршрут
    // пошлёт «»» instead would read as a bug — or worse, as a promise.
    const cmd = attrs.openCmd || renderDefaultOpen(defaultOpen, dir);
    if (cmd) {
      parts.push(`route sends '${cmd}' before the step`);
    } else {
      parts.push(
        "no opening command is set and this MUD's room format does not open doors" +
          " — the route will not open the door",
      );
    }
  } else if (attrs.openCmd) {
    // An opening command with no door is inert — say so rather than let it look armed.
    parts.push("an opening command is set, but this edge has no door — it will not run");
  }
  if (attrs.locked) {
    parts.push("locked by default from this side — a key is required");
  }
  if (attrs.noRoute) {
    parts.push("routes avoid this edge (manual movement still works and is tracked)");
  }
  if (parts.length === 0) return "";
  return parts.join(" · ");
}

// renderDefaultOpen substitutes the direction placeholders into the session's
// room-format opening template, mirroring mapper.renderDoorTemplate (Go): "{dir}"
// takes the Russian direction word, "{dir_en}" the canonical english letter, and a
// template that names neither is used as it stands. An EMPTY template stays empty —
// that is "this format does not open doors", not "open with nothing".
export function renderDefaultOpen(template: string, dir: string): string {
  // No guard for the empty template: substituting into "" yields "", which IS the
  // answer ("this format does not open doors"). A guard here would be dead code that
  // looks load-bearing — the branch that MATTERS is the one in attrsSummary, which
  // has to say so in words instead of quoting an empty command.
  const word =
    { N: "север", S: "юг", E: "восток", W: "запад", U: "вверх", D: "вниз" }[
      dir
    ] ?? dir;
  // replaceAll, not replace: Go's renderDoorTemplate uses strings.ReplaceAll, and
  // these two are declared mirrors. With `replace` a template naming a placeholder
  // twice rendered differently on the two sides — the pane would promise one command
  // and the route would send another, which is the whole class of bug this field was
  // moved out of a constant to avoid.
  const out = template
    .replaceAll("{dir_en}", dir.toLowerCase())
    .replaceAll("{dir}", word === dir ? "" : word);
  return out.trim().replace(/\s+/g, " ");
}

// One directed half of the boundary, as the popup renders it.
export interface EdgeSide {
  from: Cell;
  to: Cell;
  dir: string;
  fromLabel: string;
  toLabel: string;
  state: FaceState;
  passable: boolean;
  provenance: string;
  // The attribute layer of THIS face. Editable only where there is a passage: the
  // attributes describe how a passage is taken, so on an absent or denied face the
  // controls have nothing to describe (and the backend refuses the write).
  attrs: EdgeAttrs;
  attrsEditable: boolean;
}

// EdgeModel is the whole popup: the boundary plus its two directed halves, in a
// stable order (the cell the click was attributed to first).
export interface EdgeModel {
  a: Cell;
  b: Cell;
  sides: [EdgeSide, EdgeSide];
}

function label(room: SlimRoom | null, cell: Cell): string {
  const h = room?.h?.trim();
  if (h) return h;
  return `${cell.x},${cell.y},${cell.l}`;
}

// buildEdgeModel assembles the popup model for the boundary between `a` and its
// neighbour in `dir`. Returns null when dir is not a grid step.
export function buildEdgeModel(
  a: Cell,
  dir: string,
  roomA: SlimRoom | null,
  roomB: SlimRoom | null,
): EdgeModel | null {
  const b = neighbourCell(a, dir);
  const back = OPPOSITE[dir];
  if (!b || !back) return null;
  const sa = faceState(roomA, dir);
  const sb = faceState(roomB, back);
  const la = label(roomA, a);
  const lb = label(roomB, b);
  return {
    a,
    b,
    sides: [
      {
        from: a,
        to: b,
        dir,
        fromLabel: la,
        toLabel: lb,
        state: sa,
        passable: passableNow(sa),
        provenance: provenanceLabel(sa),
        attrs: faceAttrs(roomA, dir),
        attrsEditable: passableNow(sa),
      },
      {
        from: b,
        to: a,
        dir: back,
        fromLabel: lb,
        toLabel: la,
        state: sb,
        passable: passableNow(sb),
        provenance: provenanceLabel(sb),
        attrs: faceAttrs(roomB, back),
        attrsEditable: passableNow(sb),
      },
    ],
  };
}

// dirName renders a canonical letter as the English direction word used by the pane.
export function dirName(dir: string): string {
  return (
    { N: "north", S: "south", E: "east", W: "west", U: "up", D: "down" }[
      dir
    ] ?? dir
  );
}

// verticalMarks lists the hand-drawn marks on a cell's U/D faces. They are the
// one part of the layer the boundary popup cannot reach — a floor plan has no
// boundary between a cell and the one above it — so the cell menu shows and
// clears them instead. A mark that cannot be seen cannot be taken back.
export function verticalMarks(
  room: SlimRoom,
): Array<{ dir: string; state: FaceState }> {
  const out: Array<{ dir: string; state: FaceState }> = [];
  for (const dir of ["U", "D"]) {
    const st = faceState(room, dir);
    if (st === "open" || st === "blocked") out.push({ dir, state: st });
  }
  return out;
}
