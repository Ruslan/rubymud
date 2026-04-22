export interface Hotkey {
  shortcut: string;
  command: string;
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
  text?: string;
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
  settings?: { domain: string };
}
