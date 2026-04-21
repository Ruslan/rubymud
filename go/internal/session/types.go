package session

type ServerMsg struct {
	Type      string           `json:"type"`
	Entries   []ClientLogEntry `json:"entries,omitempty"`
	History   []string         `json:"history,omitempty"`
	Hotkeys   []HotkeyJSON     `json:"hotkeys,omitempty"`
	Variables []VariableJSON   `json:"variables,omitempty"`
	Status    string           `json:"status,omitempty"`
	Message   string           `json:"message,omitempty"`
}

type ClientLogEntry struct {
	Text     string          `json:"text"`
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
