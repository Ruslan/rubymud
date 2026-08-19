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
  bell_positions?: number[];
}

export interface ServerMessage {
  type: string;
  // Room-format status. `mapper_format` is the reference the session actually
  // honoured ("builtin:rmud", "db:7"); `mapper_profile_id` is the row id when
  // that reference is a db: one, and 0/absent for a compiled-in detector.
  // `mapper_profile_error` is non-empty only when the session's stored format
  // could not be honoured and it fell back to the default — that is the one case
  // worth showing the user.
  mapper_format?: string;
  mapper_profile_id?: number;
  mapper_profile_name?: string;
  mapper_profile_error?: string;
  // The room-format profile's door-opening command template
  // (mapper.ProfileSpec.DoorOpen), empty when this format describes none. It is a
  // property of the MUD on the other end, so the map pane must take it from here
  // rather than hold a constant — a constant would show "откр две" on a Diku.
  mapper_door_open?: string;
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
  client_command_id?: string;
  commands?: string[];
  restore_cursor?: number;
  // Mapper `room` broadcast: the live room block the parser last saw. Carries no
  // coords — the position comes separately via `room_position`.
  room_hint?: string;
  room_desc?: string;
  room_exits?: string;
  // Change-notification scope. A positive id on map_sets_changed identifies the
  // COW fork that became active; missing/zero is a generic map-set invalidation.
  map_set_id?: number;
  zone?: string;
  room_x?: number;
  room_y?: number;
  room_l?: number;
  map_recording?: {
    mode: "off" | "armed" | "recording" | "conflict";
    reason?: string;
    pending: number;
    revision: number;
  };
  // Server-owned map walk progress. A map walk is paced by room confirmations;
  // these fields are also replayed during websocket restore.
  walk_active?: boolean;
  walk_epoch?: number;
  walk_job_id?: number;
  walk_current?: number;
  walk_total?: number;
  walk_reason?: string;
  walk_state?: string;
  walk_can_resume?: boolean;
  walk_revision?: number;
}
