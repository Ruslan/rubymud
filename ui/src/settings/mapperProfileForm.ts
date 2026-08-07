import type {
  MapperBlockStop,
  MapperExitsRule,
  MapperHeaderRule,
  MapperProfile,
  MapperSpec,
  MapperStripMode,
} from './types';

// ---------------------------------------------------------------------------
// Room-format ("mapper profile") form model.
//
// The settings editor is STRUCTURED, not a JSON textarea, so it needs a flat
// form shape that maps onto the nested ProfileSpec. Two conversions live here
// and nowhere else, because they are the only non-obvious part of the section
// and the only part worth unit-testing:
//
//   specToForm — nested spec  -> flat form, filling defaults for fields the
//                spec legitimately omits (Go marshals min_run/width/column_run
//                with omitempty), so switching an enum in the UI never lands on
//                a 0 the backend would reject.
//   formToSpec — flat form -> nested spec, emitting ONLY the fields the chosen
//                enum actually uses. That mirrors Go's omitempty output, so a
//                profile saved from this form and read back produces the same
//                form again instead of drifting.
//
// VALIDATION IS NOT HERE. The backend (mapper.Compile, via
// POST /api/mapper-profiles/validate) is the single source of validation truth;
// reimplementing any of its rules in TS is how the two silently diverge. This
// module only shapes data.
// ---------------------------------------------------------------------------

export interface MapperProfileForm {
  name: string;
  // exits
  guard: string;
  pattern: string;
  none_token: string;
  exits_alt: MapperExitsRule[];
  // block
  stop_blank: boolean;
  stop_prompt: boolean;
  stop_max_lines: boolean;
  max_lines: number;
  prompt: string;
  // left-column strip
  strip_mode: MapperStripMode;
  glyphs: string;
  min_run: number;
  width: number;
  // header
  header_rule: MapperHeaderRule;
  column_run: number;
  ansi_start: string;
  // how a route opens a door on this mud (the spec's one output rule)
  door_open: string;
}

// Fallbacks for fields a spec may omit. They are the builtin's values, so a form
// opened on a partial spec starts from something that compiles rather than from
// zeros the backend rejects.
const DEFAULT_GLYPHS = '[]*^v.|_- ';
const DEFAULT_MAX_LINES = 32;
const DEFAULT_MIN_RUN = 2;
const DEFAULT_WIDTH = 12;
const DEFAULT_COLUMN_RUN = 2;

// Keep the settings UI aligned with mapper.maxExitsAlternatives. The backend is
// still authoritative; this prevents a user from building a form it will reject.
export const maxMapperExitsAlternatives = 8;

// Canonical stop order. Go preserves the order it is given, so emitting a stable
// one keeps a saved spec byte-comparable with the same profile saved twice.
const STOP_ORDER: MapperBlockStop[] = ['blank', 'prompt', 'max_lines'];

export function emptyMapperProfileForm(): MapperProfileForm {
  return {
    name: '',
    guard: '',
    pattern: '',
    none_token: 'none',
    exits_alt: [],
    stop_blank: true,
    stop_prompt: false,
    stop_max_lines: true,
    max_lines: DEFAULT_MAX_LINES,
    prompt: '',
    strip_mode: 'greedy',
    glyphs: DEFAULT_GLYPHS,
    min_run: DEFAULT_MIN_RUN,
    width: DEFAULT_WIDTH,
    header_rule: 'last_before_stripped',
    column_run: DEFAULT_COLUMN_RUN,
    ansi_start: '',
    // Empty by default, on purpose: a new format describes no door verb until its
    // author says otherwise, and that is the answer that is never wrong.
    door_open: '',
  };
}

function num(value: unknown, fallback: number): number {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

export function specToForm(spec: MapperSpec | undefined, name = ''): MapperProfileForm {
  const form = emptyMapperProfileForm();
  if (!spec) return { ...form, name };
  const stops = spec.block?.stops || [];
  return {
    name: name || spec.name || '',
    guard: spec.exits?.guard ?? '',
    pattern: spec.exits?.pattern ?? '',
    none_token: spec.exits?.none_token ?? 'none',
    exits_alt: (spec.exits?.alt ?? []).map((rule) => ({ ...rule })),
    stop_blank: stops.includes('blank'),
    stop_prompt: stops.includes('prompt'),
    stop_max_lines: stops.includes('max_lines'),
    max_lines: num(spec.block?.max_lines, DEFAULT_MAX_LINES),
    prompt: spec.prompt ?? '',
    strip_mode: spec.strip?.mode ?? 'greedy',
    glyphs: spec.strip?.glyphs ?? DEFAULT_GLYPHS,
    min_run: num(spec.strip?.min_run, DEFAULT_MIN_RUN),
    width: num(spec.strip?.width, DEFAULT_WIDTH),
    header_rule: spec.header?.rule ?? 'last_before_stripped',
    column_run: num(spec.header?.column_run, DEFAULT_COLUMN_RUN),
    ansi_start: spec.header?.ansi_start ?? '',
    door_open: spec.door_open ?? '',
  };
}

export function formToSpec(form: MapperProfileForm): MapperSpec {
  const stops = STOP_ORDER.filter((s) =>
    s === 'blank' ? form.stop_blank : s === 'prompt' ? form.stop_prompt : form.stop_max_lines,
  );

  // Emit only the length field the chosen mode uses (min_run has no width and
  // vice versa), but ALWAYS emit glyphs. Glyphs are not a strip-only field: the
  // last_before_stripped header rule locates the room name through them in every
  // strip mode, so blanking them under fixed_width compiles fine and then makes
  // every room name the movement preamble above it. It is also lossy — opening a
  // fixed_width profile that carries glyphs and saving it would erase them.
  const strip: MapperSpec['strip'] =
    form.strip_mode === 'fixed_width'
      ? { mode: 'fixed_width', glyphs: form.glyphs, width: Number(form.width) }
      : form.strip_mode === 'min_run'
        ? { mode: 'min_run', glyphs: form.glyphs, min_run: Number(form.min_run) }
        : { mode: 'greedy', glyphs: form.glyphs };

  const header: MapperSpec['header'] = form.header_rule === 'first'
    ? { rule: 'first' }
    : form.header_rule === 'ansi_delimited'
      ? { rule: 'ansi_delimited', ansi_start: form.ansi_start }
      : { rule: 'last_before_stripped', column_run: Number(form.column_run) };

  const spec: MapperSpec = {
    name: form.name.trim(),
    exits: {
      guard: form.guard,
      pattern: form.pattern,
      none_token: form.none_token,
    },
    block: { stops, max_lines: Number(form.max_lines) },
    strip,
    header,
  };
  if (form.exits_alt.length > 0) {
    spec.exits.alt = form.exits_alt.map((rule) => ({ ...rule }));
  }
  // The prompt is only meaningful when set; an empty one is omitted so the
  // stored spec matches Go's `omitempty` shape.
  if (form.prompt) spec.prompt = form.prompt;
  // Same omitempty shape as the prompt, and the same reason: an empty door command
  // is not "open with nothing", it is "this format does not describe doors", which
  // is what every format but RMUD's says.
  if (form.door_open) spec.door_open = form.door_open;
  return spec;
}

// copyOfMapperProfile pre-fills the editor from an existing profile — the
// "Make a copy" action, and the ONLY way to base a profile on a declarative
// builtin (which has no row to edit). The name is suffixed so the create call
// cannot collide with the source's UNIQUE name.
//
// A contributed Go detector has NO spec, so there is nothing to copy. The caller
// is expected to have hidden the action (mapperProfileEditable), but this
// degrades to an empty form rather than throwing if it did not: a TypeError here
// would blank the whole settings pane.
export function copyOfMapperProfile(profile: MapperProfile): MapperProfileForm {
  if (!profile.spec) return emptyMapperProfileForm();
  // The suffix is not cosmetic: `name` is UNIQUE, so a copy that kept the source
  // name would fail on save. Hyphenated (`rmud-copy`) rather than spaced so the
  // prefilled value is a plausible final name and Add succeeds on the first
  // click, instead of greeting the user with a validation error.
  return specToForm(profile.spec, `${profile.name}-copy`);
}

// mapperProfileLabel is what the per-session selector and the list show.
//
// Each entry has both a KEY (`name`, stable, what the reference
// stores) and a human TITLE. The label leads with the title when there is one,
// because "Diku-family (English) — starter" tells a user what they are choosing
// and "diku-en" does not. The key is not lost: the Room Formats table has a
// Reference column and the session selector appends the reference to each option,
// so a warning quoting "builtin:diku-en" is traceable back to a row.
export function mapperProfileLabel(profile: MapperProfile): string {
  const label = profile.title && profile.title !== profile.name ? profile.title : profile.name;
  return profile.builtin ? `${label} (builtin)` : label;
}

// mapperProvenanceNote is the one-line honesty label next to a format's name.
// These strings are the UI half of mapper.Provenance and must keep saying what
// the Go constants say — a user picking a format needs to know whether anyone
// has ever run it against a real server.
export function mapperProvenanceNote(profile: MapperProfile): string {
  switch (profile.provenance) {
    case 'Verified':
      return 'Verified — field-tested against a live server.';
    case 'Starter':
      return 'Starter — a plausible format nobody has run against a live server. Copy it and adjust.';
    case 'Contributed':
      return 'Contributed Go detector — correctness is exactly its test fixtures\'.';
    case 'User':
      return 'Your own format.';
    default:
      return '';
  }
}
