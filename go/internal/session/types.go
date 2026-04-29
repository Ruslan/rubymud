package session

type ServerMsg struct {
	Type      string                        `json:"type"`
	Buffers   map[string][]ClientLogEntry   `json:"buffers,omitempty"`
	Entries   []ClientLogEntry              `json:"entries,omitempty"`
	History   []string                      `json:"history,omitempty"`
	Hotkeys   []HotkeyJSON                  `json:"hotkeys,omitempty"`
	Variables []VariableJSON                `json:"variables,omitempty"`
	Status    string                        `json:"status,omitempty"`
	Message   string                        `json:"message,omitempty"`
	Settings  *SettingsChangedJSON          `json:"settings,omitempty"`
	EntryID   int64                         `json:"entry_id,omitempty"` // For targeted updates
	Command   string                        `json:"command,omitempty"`  // For targeted updates
	Buffer    string                        `json:"buffer,omitempty"`   // For targeted updates
	Timers    []TimerSnapshot               `json:"timers,omitempty"`
}

type SettingsChangedJSON struct {
	Domain string `json:"domain"`
}

type ClientLogEntry struct {
	ID       int64           `json:"id,omitempty"`
	Text     string          `json:"text"`
	Buffer   string          `json:"buffer,omitempty"`
	Commands []string        `json:"commands,omitempty"`
	Buttons  []ButtonOverlay `json:"buttons,omitempty"`
}

type ButtonOverlay struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type HotkeyJSON struct {
	Shortcut    string `json:"shortcut"`
	Command     string `json:"command"`
	MobileRow   int    `json:"mobile_row"`
	MobileOrder int    `json:"mobile_order"`
}

type VariableJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
