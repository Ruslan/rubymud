package session

type ServerMsg struct {
	Type            string                      `json:"type"`
	Buffers         map[string][]ClientLogEntry `json:"buffers,omitempty"`
	Entries         []ClientLogEntry            `json:"entries,omitempty"`
	History         []string                    `json:"history,omitempty"`
	Hotkeys         []HotkeyJSON                `json:"hotkeys,omitempty"`
	Variables       []VariableJSON              `json:"variables,omitempty"`
	Status          string                      `json:"status,omitempty"`
	Message         string                      `json:"message,omitempty"`
	Settings        *SettingsChangedJSON        `json:"settings,omitempty"`
	EntryID         int64                       `json:"entry_id,omitempty"` // For targeted updates
	Command         string                      `json:"command,omitempty"`  // For targeted updates
	Buffer          string                      `json:"buffer,omitempty"`   // For targeted updates
	Timers          []TimerSnapshot             `json:"timers,omitempty"`
	ClientCommandID string                      `json:"client_command_id,omitempty"`
	Commands        []string                    `json:"commands,omitempty"`
	RestoreCursor   *int64                      `json:"restore_cursor,omitempty"`

	// Mapper current-room signal (Type:"room") and tracker position
	// (Type:"room_position"). All omitempty so non-mapper messages are unaffected.
	RoomHint       string `json:"room_hint,omitempty"`
	RoomDesc       string `json:"room_desc,omitempty"`
	RoomExits      string `json:"room_exits,omitempty"`
	Confidence     string `json:"confidence,omitempty"`
	PendingMoves   int    `json:"pending_moves,omitempty"`
	PositionValid  bool   `json:"position_valid,omitempty"`
	PositionReason string `json:"position_reason,omitempty"`
	Zone           string `json:"zone,omitempty"`
	RoomX          int    `json:"room_x,omitempty"`
	RoomY          int    `json:"room_y,omitempty"`
	RoomL          int    `json:"room_l,omitempty"`
	IsDT           bool   `json:"is_dt,omitempty"`
	Pipe           bool   `json:"pipe,omitempty"`
	// Exit diff surfaced when the tracker assumed a cell on a room-event mismatch
	// (yellow): exits_added_live = in the game but not the map; exits_removed_map =
	// in the map but not the game. Feeds the UI hover-diff / map-patch tool.
	ExitsAddedLive  []string `json:"exits_added_live,omitempty"`
	ExitsRemovedMap []string `json:"exits_removed_map,omitempty"`
}

type SettingsChangedJSON struct {
	Domain string `json:"domain"`
}

type ClientLogEntry struct {
	ID            int64           `json:"id,omitempty"`
	Text          string          `json:"text"`
	Buffer        string          `json:"buffer,omitempty"`
	Commands      []string        `json:"commands,omitempty"`
	Buttons       []ButtonOverlay `json:"buttons,omitempty"`
	BellPositions []int           `json:"bell_positions,omitempty"`
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
