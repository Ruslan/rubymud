import { describe, expect, it } from 'vitest';

import {
  copyOfMapperProfile,
  emptyMapperProfileForm,
  formToSpec,
  mapperProfileLabel,
  mapperProvenanceNote,
  specToForm,
} from './mapperProfileForm';
import { mapperProfileEditable, mapperProfileRef } from './types';
import type { MapperProfile, MapperSpec } from './types';

// The builtin RMUD spec exactly as GET /api/mapper-profiles returns it.
const rmudSpec: MapperSpec = {
  name: 'rmud',
  exits: { guard: '[ Exits:', pattern: '^\\[ Exits:\\s*(.*?)\\s*\\]$', none_token: 'none' },
  block: { stops: ['blank', 'max_lines'], max_lines: 32 },
  strip: { mode: 'greedy', glyphs: '[]*^v.|_- ' },
  header: { rule: 'last_before_stripped', column_run: 2 },
  obscured_lines: ['Здесь кромешная тьма.'],
};

// Every shape the editor has to survive a load-then-save on. Testing only the
// builtin is what let the fixed_width glyph-blanking bug through: the builtin is
// greedy, so it never exercised the branch that dropped the field.
const roundTripCases: Array<{ name: string; spec: MapperSpec }> = [
  { name: 'builtin (greedy, last_before_stripped)', spec: rmudSpec },
  {
    name: 'fixed_width with glyphs + last_before_stripped',
    spec: {
      name: 'fw-header',
      exits: { guard: 'Obvious exits:', pattern: '^Obvious exits:\\s*(.*?)\\s*\\.?$', none_token: 'None' },
      block: { stops: ['blank', 'max_lines'], max_lines: 16 },
      strip: { mode: 'fixed_width', glyphs: '[]*^v.|_- ', width: 12 },
      header: { rule: 'last_before_stripped', column_run: 2 },
    },
  },
  {
    name: 'min_run + header:first + prompt stop',
    spec: {
      name: 'english',
      exits: {
        guard: 'Obvious exits:',
        pattern: '^Obvious exits:\\s*(.*?)\\s*\\.?$',
        none_token: 'None',
        alt: [
          { guard: 'Ways:', pattern: '^Ways:\\s*(.*?)\\s*$' },
          { guard: 'No exits', pattern: '^No exits()$' },
        ],
      },
      block: { stops: ['blank', 'prompt', 'max_lines'], max_lines: 16 },
      prompt: '^<\\d+hp \\d+m>$',
      strip: { mode: 'min_run', glyphs: '[]*^v.|_- ', min_run: 2 },
      header: { rule: 'first' },
    },
  },
  {
    name: 'fixed_width + header:first (glyphs still carried, never silently dropped)',
    spec: {
      name: 'fw-first',
      exits: { guard: 'Exits:', pattern: '^Exits:(.*)$', none_token: 'none' },
      block: { stops: ['prompt'], max_lines: 8 },
      prompt: '^>$',
      strip: { mode: 'fixed_width', glyphs: 'abc', width: 20 },
      header: { rule: 'first' },
    },
  },
];

describe('mapper profile form <-> spec', () => {
  // Opening a profile in the editor and saving it back must not mutate a single
  // rule. Every parsed description — and therefore every map fingerprint —
  // depends on these values.
  it.each(roundTripCases)('round-trips $name unchanged', ({ spec }) => {
    expect(formToSpec(specToForm(spec))).toEqual(spec);
  });

  it('keeps the glyph set under fixed_width, because the header rule reads it', () => {
    // THE regression. Glyphs are not strip-only: last_before_stripped locates the
    // room name through them in every strip mode. Blanking them here compiles
    // clean (so the form says «Формат корректен») and then makes every room name
    // the movement preamble above it — a mapper that silently stops working.
    const form = specToForm(rmudSpec);
    form.strip_mode = 'fixed_width';
    form.width = 12;
    const spec = formToSpec(form);
    expect(spec.strip.mode).toBe('fixed_width');
    expect(spec.strip.glyphs).toBe(rmudSpec.strip.glyphs);
    expect(spec.header.rule).toBe('last_before_stripped');
  });

  it('maps the stops array to checkboxes and back in a stable order', () => {
    const form = specToForm(rmudSpec);
    expect(form.stop_blank).toBe(true);
    expect(form.stop_max_lines).toBe(true);
    expect(form.stop_prompt).toBe(false);

    form.stop_prompt = true;
    form.prompt = '^<\\d+hp>$';
    // Canonical order regardless of which boxes were ticked when.
    expect(formToSpec(form).block.stops).toEqual(['blank', 'prompt', 'max_lines']);

    form.stop_blank = false;
    form.stop_max_lines = false;
    expect(formToSpec(form).block.stops).toEqual(['prompt']);
  });

  it('round-trips an optional six-direction server scan and omits it when disabled', () => {
    const spec: MapperSpec = {
      ...rmudSpec,
      scan: {
        command: 'scan',
        directions: [
          { dir: 'N', prefix: 'На севере:' }, { dir: 'E', prefix: 'На востоке:' },
          { dir: 'S', prefix: 'На юге:' }, { dir: 'W', prefix: 'На западе:' },
          { dir: 'U', prefix: 'Наверху:' }, { dir: 'D', prefix: 'Внизу:' },
        ],
      },
    };
    const form = specToForm(spec);
    expect(form.scan_command).toBe('scan');
    expect(form.scan_prefix_u).toBe('Наверху:');
    expect(form.scan_prefix_d).toBe('Внизу:');
    expect(formToSpec(form)).toEqual(spec);

    const disabled = emptyMapperProfileForm();
    expect(formToSpec(disabled).scan).toBeUndefined();
  });

  it('round-trips exact obscured-room lines and omits an empty list', () => {
    const form = specToForm(rmudSpec);
    expect(form.obscured_lines).toBe('Здесь кромешная тьма.');
    form.obscured_lines = ' Здесь кромешная тьма. \n You cannot see anything. ';
    expect(formToSpec(form).obscured_lines).toEqual(['Здесь кромешная тьма.', 'You cannot see anything.']);
    form.obscured_lines = ' \n ';
    expect(formToSpec(form).obscured_lines).toBeUndefined();
  });

  it('round-trips and independently copies alternative exits rules', () => {
    const source = roundTripCases[2].spec;
    const form = specToForm(source);
    expect(form.exits_alt).toEqual(source.exits.alt);
    form.exits_alt[0].guard = 'Changed:';
    expect(source.exits.alt?.[0].guard).toBe('Ways:');
    expect(formToSpec(form).exits.alt?.[0].guard).toBe('Changed:');
  });

  it('emits only the LENGTH field the selected mode uses, and always the glyphs', () => {
    const form = emptyMapperProfileForm();

    form.strip_mode = 'greedy';
    expect(formToSpec(form).strip).toEqual({ mode: 'greedy', glyphs: '[]*^v.|_- ' });

    form.strip_mode = 'min_run';
    form.min_run = 3;
    expect(formToSpec(form).strip).toEqual({ mode: 'min_run', glyphs: '[]*^v.|_- ', min_run: 3 });

    // fixed_width cuts at a column and ignores glyphs when cutting — but still
    // carries them, for the header rule.
    form.strip_mode = 'fixed_width';
    form.width = 14;
    expect(formToSpec(form).strip).toEqual({ mode: 'fixed_width', glyphs: '[]*^v.|_- ', width: 14 });
    expect(formToSpec(form).strip.min_run).toBeUndefined();
  });

  it('emits column_run only for the last_before_stripped header rule', () => {
    const form = emptyMapperProfileForm();

    form.header_rule = 'last_before_stripped';
    form.column_run = 4;
    expect(formToSpec(form).header).toEqual({ rule: 'last_before_stripped', column_run: 4 });

    form.header_rule = 'first';
    expect(formToSpec(form).header).toEqual({ rule: 'first' });

    form.header_rule = 'ansi_delimited';
    form.ansi_start = '1;36';
    expect(formToSpec(form).header).toEqual({ rule: 'ansi_delimited', ansi_start: '1;36' });
  });

  it('round-trips an ANSI-delimited room header', () => {
    const spec: MapperSpec = {
      ...rmudSpec,
      name: 'ansi-room-name',
      header: { rule: 'ansi_delimited', ansi_start: '38;5;45' },
    };
    const form = specToForm(spec);
    expect(form.header_rule).toBe('ansi_delimited');
    expect(form.ansi_start).toBe('38;5;45');
    expect(formToSpec(form)).toEqual(spec);
  });

  it('omits an empty prompt but keeps a set one', () => {
    const form = emptyMapperProfileForm();
    expect(formToSpec(form).prompt).toBeUndefined();
    form.prompt = '^<\\d+hp \\d+m>$';
    expect(formToSpec(form).prompt).toBe('^<\\d+hp \\d+m>$');
  });

  it('fills defaults for fields the spec omits, so switching an enum never yields 0', () => {
    // Go marshals min_run/width/column_run with omitempty, so a greedy profile's
    // spec carries none of them. Selecting fixed_width in the form must not then
    // submit width: 0, which the backend rejects.
    const form = specToForm(rmudSpec);
    expect(form.width).toBeGreaterThan(0);
    expect(form.min_run).toBeGreaterThan(0);

    const headerless = specToForm({ ...rmudSpec, header: { rule: 'first' } });
    expect(headerless.column_run).toBeGreaterThan(0);
    headerless.header_rule = 'last_before_stripped';
    expect(formToSpec(headerless).header.column_run).toBeGreaterThan(0);
  });

  it('trims the name and mirrors it into the spec', () => {
    // The backend forces spec.name to the row name; sending them already in sync
    // means the form does not show one name and store another.
    const form = { ...emptyMapperProfileForm(), name: '  Tortilla  ' };
    expect(formToSpec(form).name).toBe('Tortilla');
  });

  it('copies a profile under a new, immediately-savable name', () => {
    // The suffix matters twice: `name` is UNIQUE so the copy cannot reuse it, and
    // a prefilled name means Add succeeds on the first click instead of greeting
    // the user with a validation error on a nameless format.
    const builtin: MapperProfile = { id: 0, name: 'rmud', builtin: true, version: 1, spec: rmudSpec };
    const copy = copyOfMapperProfile(builtin);
    expect(copy.name).toBe('rmud-copy');
    expect(copy.name.trim()).not.toBe('');
    // Everything else is the builtin verbatim — a copy must parse identically.
    expect(formToSpec({ ...copy, name: 'rmud' })).toEqual(rmudSpec);
  });

  it('labels the builtin as read-only in selectors', () => {
    const builtin: MapperProfile = { id: 0, name: 'rmud', builtin: true, version: 1, spec: rmudSpec };
    const stored: MapperProfile = { id: 3, name: 'tortilla', builtin: false, version: 1, spec: rmudSpec };
    expect(mapperProfileLabel(builtin)).toBe('rmud (builtin)');
    expect(mapperProfileLabel(stored)).toBe('tortilla');
  });

  // --- references, Go detectors, provenance --------------------------------

  it('prefers the human title over the key in selector labels', () => {
    const diku: MapperProfile = {
      ref: 'builtin:diku-en', id: 0, name: 'diku-en', builtin: true, version: 1,
      spec: rmudSpec, title: 'Diku-family (English) — starter', provenance: 'Starter', editable: true,
    };
    expect(mapperProfileLabel(diku)).toBe('Diku-family (English) — starter (builtin)');
  });

  it('identifies an entry by its reference, with a fallback for an older backend', () => {
    const withRef: MapperProfile = { ref: 'builtin:rmud', id: 0, name: 'rmud', builtin: true, version: 1, spec: rmudSpec };
    expect(mapperProfileRef(withRef)).toBe('builtin:rmud');
    // A response from an older backend carries no `ref`; the id/builtin pair still
    // determines it, so an older backend does not break the selector.
    const legacyBuiltin: MapperProfile = { id: 0, name: 'rmud', builtin: true, version: 1, spec: rmudSpec };
    const legacyRow: MapperProfile = { id: 7, name: 'mine', builtin: false, version: 1, spec: rmudSpec };
    expect(mapperProfileRef(legacyBuiltin)).toBe('builtin:rmud');
    expect(mapperProfileRef(legacyRow)).toBe('db:7');
  });

  it('treats a contributed Go detector as non-editable and refuses to fake a copy', () => {
    // A Go detector has NO spec. The UI must not offer "make a copy" — and if it
    // somehow does, copyOfMapperProfile must yield an empty form rather than
    // throwing on `profile.spec`, which would blank the settings pane.
    const go: MapperProfile = {
      ref: 'builtin:_example', id: 0, name: '_example', builtin: true, version: 1,
      title: 'Example contributed detector', provenance: 'Contributed',
      editable: false, go_detector: true,
    };
    expect(mapperProfileEditable(go)).toBe(false);
    expect(copyOfMapperProfile(go)).toEqual(emptyMapperProfileForm());

    const declarative: MapperProfile = {
      ref: 'builtin:rmud', id: 0, name: 'rmud', builtin: true, version: 1, spec: rmudSpec, editable: true,
    };
    expect(mapperProfileEditable(declarative)).toBe(true);
  });

  it('states provenance honestly, including that a Starter has never been run', () => {
    const mk = (provenance: string): MapperProfile =>
      ({ id: 0, name: 'x', builtin: true, version: 1, spec: rmudSpec, provenance });
    expect(mapperProvenanceNote(mk('Verified'))).toContain('field-tested');
    expect(mapperProvenanceNote(mk('Starter'))).toContain('nobody has run');
    expect(mapperProvenanceNote(mk('Contributed'))).toContain('fixtures');
    expect(mapperProvenanceNote(mk('User'))).toContain('Your own');
    // An unknown provenance renders as nothing rather than as something
    // reassuring the backend never said.
    expect(mapperProvenanceNote(mk('Official'))).toBe('');
  });
});

// The door-opening command is a spec field like any other, and the settings editor
// is where a user says how THEIR mud opens doors (AGENTS #2: a new entity has to be
// manageable from the UI). It round-trips with the same omitempty shape as `prompt`,
// because an empty value is meaningful — "this format does not describe doors" —
// and must not be stored as an empty string the backend would then render.
describe('door-opening command round-trip', () => {
  it('carries a template through form -> spec -> form', () => {
    const form = { ...emptyMapperProfileForm(), name: 'rmud-copy', guard: '[ Exits:', pattern: '^\\[ Exits:\\s*(.*?)\\s*\\]$', door_open: 'откр две {dir}' };
    const spec = formToSpec(form);
    expect(spec.door_open).toBe('откр две {dir}');
    expect(specToForm(spec).door_open).toBe('откр две {dir}');
  });

  it('omits an empty one, matching the backend omitempty shape', () => {
    const spec = formToSpec({ ...emptyMapperProfileForm(), name: 'x', guard: 'g', pattern: '(.*)' });
    expect('door_open' in spec).toBe(false);
    expect(specToForm(spec).door_open).toBe('');
  });

  it('defaults to empty for a brand-new format', () => {
    expect(emptyMapperProfileForm().door_open).toBe('');
  });
});
