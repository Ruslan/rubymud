export interface RuleGroup {
  group_name: string;
  total_count: number;
  enabled_count: number;
  disabled_count: number;
}

export interface TimerSnapshot {
  name: string;
  enabled: boolean;
  cycle_ms: number;
  next_tick_at: string;
  remaining_ms: number;
  icon?: string;
  repeat_mode?: string;
}

export interface Hotkey {
  shortcut: string;
  command: string;
  mobile_row?: number;
  mobile_order?: number;
}

export interface Variable {
  key: string;
  value: string;
}

export interface ResolvedVariable {
  name: string;
  value: string;
  default_value: string;
  declared: boolean;
  has_value: boolean;
  uses_default: boolean;
}


export interface ButtonOverlay {
  label: string;
  command: string;
}

export interface LogEntry {
  id?: number;
  text?: string;
  buffer?: string;
  commands?: string[];
  buttons?: ButtonOverlay[];
}

export interface ServerMessage {
  type: string;
  status?: string;
  history?: string[];
  hotkeys?: Hotkey[];
  variables?: Variable[];
  entries?: LogEntry[];
  buffers?: Record<string, LogEntry[]>;
  settings?: { domain: string };
  // Targeted updates
  entry_id?: number;
  command?: string;
  buffer?: string;
  timers?: TimerSnapshot[];
}
