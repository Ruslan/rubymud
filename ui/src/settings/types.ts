export interface Variable {
  key: string;
  value: string;
}

export interface Profile {
  id: number;
  name: string;
  description: string;
}

export interface SessionProfile {
  id: number;
  session_id: number;
  profile_id: number;
  order_index: number;
  profile_name: string;
}

export interface Alias {
  id?: number;
  position?: number;
  name: string;
  template: string;
  enabled: boolean;
  group_name: string;
}

export interface Trigger {
  id?: number;
  position?: number;
  name: string;
  pattern: string;
  command: string;
  enabled: boolean;
  is_button: boolean;
  stop_after_match: boolean;
  group_name: string;
  target_buffer?: string;
  buffer_action?: string;
}

export interface Highlight {
  id?: number;
  position?: number;
  pattern: string;
  fg: string;
  bg: string;
  bold: boolean;
  faint: boolean;
  italic: boolean;
  underline: boolean;
  strikethrough: boolean;
  blink: boolean;
  reverse: boolean;
  enabled: boolean;
  group_name: string;
}

export interface Substitute {
  id?: number;
  position?: number;
  pattern: string;
  replacement: string;
  is_gag: boolean;
  enabled: boolean;
  group_name: string;
}

export interface Hotkey {
  id?: number;
  position?: number;
  shortcut: string;
  command: string;
  mobile_row: number;
  mobile_order: number;
}

export interface ProfileTimerSubscription {
  profile_id: number;
  timer_name: string;
  second: number;
  sort_order: number;
  command: string;
  is_removal: boolean;
  is_bulk: boolean;
}

export interface ProfileTimer {
  profile_id: number;
  name: string;
  icon: string;
  cycle_ms: number;
  repeat_mode: string;
  subscriptions: ProfileTimerSubscription[];
}

export interface ProfileVariable {
  id?: number;
  position?: number;
  name: string;
  default_value: string;
  description: string;
}

export interface NamedColor {
  name: string;
  hex: string;
  ansi_fg: number;
  ansi_bg: number;
  ansi256: number;
}

export interface RuleGroupSummary {
  group_name: string;
  total_count: number;
  enabled_count: number;
  disabled_count: number;
}

export interface Session {
  id: number;
  name: string;
  mud_host: string;
  mud_port: number;
  status: string;
  initial_commands: string;
  ansi_theme: string;
  timezone: string;
  tz_follow: number;
  mccp_enabled: number;
  mccp_active?: boolean;
  mccp_compressed_bytes?: number;
  mccp_decompressed_bytes?: number;
  mccp_compression_ratio?: string;
}

export interface LogEntry {
  id: number;
  buffer: string;
  display_plain: string;
  plain_text: string;
  created_at: string;
}

export interface HistoryEntry {
  id: number;
  session_id: number;
  kind: string;
  line: string;
  created_at: string;
}

export interface HistoryListResponse {
  entries: HistoryEntry[];
  has_more: boolean;
  next_before_id?: number | null;
}

// --- Room-format profiles ("mapper profiles") -------------------------------
// A ROOM FORMAT describes how to parse a room block out of the MUD's output.
// It is NOT the `Profile` above (TinTin alias/trigger layering) — the two are
// unrelated entities. Field names mirror Go's mapper.ProfileSpec exactly.

export type MapperStripMode = 'greedy' | 'min_run' | 'fixed_width';
export type MapperHeaderRule = 'last_before_stripped' | 'first' | 'ansi_delimited';
export type MapperBlockStop = 'blank' | 'prompt' | 'max_lines';

export interface MapperExitsRule {
  guard: string;
  pattern: string;
}

export interface MapperSpecExits {
  guard: string;
  pattern: string;
  none_token: string;
  alt?: MapperExitsRule[];
}

export interface MapperSpecBlock {
  stops: MapperBlockStop[];
  max_lines: number;
}

export interface MapperSpecStrip {
  mode: MapperStripMode;
  glyphs: string;
  min_run?: number;
  width?: number;
}

export interface MapperSpecHeader {
  rule: MapperHeaderRule;
  column_run?: number;
  ansi_start?: string;
}

export type MapperScanDirection = 'N' | 'E' | 'S' | 'W' | 'U' | 'D';

export interface MapperSpecScan {
  command: string;
  directions: Array<{ dir: MapperScanDirection; prefix: string }>;
}

export interface MapperSpec {
  name: string;
  exits: MapperSpecExits;
  block: MapperSpecBlock;
  prompt?: string;
  strip: MapperSpecStrip;
  header: MapperSpecHeader;
  scan?: MapperSpecScan;
  // The command a computed ROUTE sends to open a door it has no per-edge command
  // for, "{dir}" / "{dir_en}" standing for the hop's direction. Like the scan
  // probe above, it is here because the format
  // is the one thing in the schema that says "this is how THIS mud talks" — it is
  // not an interface string, so it does not belong to the UI language.
  door_open?: string;
}

// MapperProfile is one entry of GET /api/mapper-profiles — a room format the
// user can select. The list holds THREE kinds of thing behind one
// shape, which is the point: the selector renders them identically.
//
//   1. A declarative builtin (rmud, diku-en): `builtin`, `editable`, has a spec.
//      Lives in Go, not in the DB, so it can only be copied, never edited.
//   2. A contributed Go detector: `builtin`, `go_detector`, NO spec, NOT
//      editable. There is nothing to copy — the UI must say so rather than
//      producing an empty format.
//   3. A user's stored row: not builtin, editable, has a spec.
//
// `ref` is the identity: it is exactly what gets written to the session's
// mapper_format column ("builtin:rmud", "db:7"). `id` is 0 for every builtin, so
// it can NOT distinguish them — key off `ref`.
//
// The DetectorInfo fields are optional so a UI built against a newer backend
// still renders against an older one (and so the existing unit-test fixtures
// stay valid); every render path below has a fallback.
export interface MapperProfile {
  ref?: string;
  id: number;
  name: string;
  builtin: boolean;
  version: number;
  /** Absent for a contributed Go detector: it has no declarative spec. */
  spec?: MapperSpec;
  title?: string;
  mud?: string;
  /** 'Verified' | 'Starter' | 'Contributed' | 'User' — shown verbatim. */
  provenance?: string;
  description?: string;
  editable?: boolean;
	go_detector?: boolean;
}

export interface MapperReplaySample {
  log_id: number;
  hint: string;
  desc: string;
  exits: string;
}

export interface MapperReplayResult {
  ok: boolean;
  session_id: number;
  scanned_lines: number;
  candidate_lines: number;
  matched_blocks: number;
  samples: MapperReplaySample[];
}

/** mapperProfileRef is the identity of a list entry, with a fallback for a
 *  response from a backend that predates the `ref` field. */
export function mapperProfileRef(p: MapperProfile): string {
  if (p.ref) return p.ref;
  return p.builtin ? `builtin:${p.name}` : `db:${p.id}`;
}

/** mapperProfileEditable: can this entry's spec be opened in the editor or
 *  copied into a new format? False for a Go detector, which has no spec. */
export function mapperProfileEditable(p: MapperProfile): boolean {
  if (p.editable !== undefined) return p.editable;
  return !!p.spec;
}
