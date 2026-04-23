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
}

type SettingsChangedJSON struct {
	Domain string `json:"domain"`
}

type ClientLogEntry struct {
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
	Shortcut string `json:"shortcut"`
	Command  string `json:"command"`
}

type VariableJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
