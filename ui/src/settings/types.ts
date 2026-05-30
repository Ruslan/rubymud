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
