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
	RoomHint             string         `json:"room_hint,omitempty"`
	RoomDesc             string         `json:"room_desc,omitempty"`
	RoomExits            string         `json:"room_exits,omitempty"`
	Confidence           string         `json:"confidence,omitempty"`
	PendingMoves         int            `json:"pending_moves,omitempty"`
	PositionValid        bool           `json:"position_valid,omitempty"`
	PositionReason       string         `json:"position_reason,omitempty"`
	PositionReasonCode   string         `json:"position_reason_code,omitempty"`
	PositionReasonArgs   map[string]any `json:"position_reason_args,omitempty"`
	PositionReasonDetail string         `json:"position_reason_detail,omitempty"`
	// MapSetID scopes write-path change notifications (rooms_changed /
	// map_sets_changed) to a specific set so a map pane bound to a now-frozen
	// original can filter — e.g. detect a fork moved the session to a new set.
	MapSetID int64  `json:"map_set_id,omitempty"`
	Zone     string `json:"zone,omitempty"`
	RoomX    int    `json:"room_x,omitempty"`
	RoomY    int    `json:"room_y,omitempty"`
	RoomL    int    `json:"room_l,omitempty"`
	IsDT     bool   `json:"is_dt,omitempty"`
	Pipe     bool   `json:"pipe,omitempty"`
	// Exit diff surfaced when the tracker assumed a cell on a room-event mismatch
	// (yellow): exits_added_live = in the game but not the map; exits_removed_map =
	// in the map but not the game. Feeds the UI hover-diff / map-patch tool.
	ExitsAddedLive  []string `json:"exits_added_live,omitempty"`
	ExitsRemovedMap []string `json:"exits_removed_map,omitempty"`
	// EventDescribesCell mirrors mapper.Position.EventDescribesCell: whether the
	// room-event behind this position actually describes the cell at the coord.
	// The map pane uses it to disable «Записать комнату» (the backend re-checks it
	// authoritatively — this is UX, not the gate).
	EventDescribesCell bool               `json:"event_describes_cell,omitempty"`
	MapRecording       *MapRecordingState `json:"map_recording,omitempty"`
	// Ephemeral, profile-parsed surroundings for the current confirmed frontier.
	// These are display hints only: they are never persisted or used as identity.
	RoomScan                  []RoomScanHint `json:"room_scan,omitempty"`
	RoomScanRecordingRevision uint64         `json:"room_scan_recording_revision,omitempty"`

	// Room-format status (Type:"mapper_profile"), broadcast whenever the session
	// (re)builds its detector.
	//
	// MapperFormat is the reference the session ACTUALLY honoured
	// ("builtin:rmud", "db:7") — on a fallback that is the default's, not the
	// stored one. MapperProfileID is the mapper_profiles row id for a db:
	// reference and 0 for a compiled-in detector; it is kept alongside the
	// reference so the settings UI can continue per-row highlighting while the
	// stored column uses the new reference format.
	//
	// MapperProfileError is non-empty ONLY when the stored assignment could not be
	// honoured and the session fell back to the default — a broken format must be
	// visible rather than silently mapping with the wrong rules. An empty (and so
	// omitted) error IS the healthy state, which is why the client keys off its
	// presence rather than comparing it to anything.
	MapperFormat             string         `json:"mapper_format,omitempty"`
	MapperProfileID          int64          `json:"mapper_profile_id,omitempty"`
	MapperProfileName        string         `json:"mapper_profile_name,omitempty"`
	MapperProfileError       string         `json:"mapper_profile_error,omitempty"`
	MapperProfileErrorCode   string         `json:"mapper_profile_error_code,omitempty"`
	MapperProfileErrorArgs   map[string]any `json:"mapper_profile_error_args,omitempty"`
	MapperProfileErrorDetail string         `json:"mapper_profile_error_detail,omitempty"`
	// MapperDoorOpen is the room-format profile's door-opening command template
	// (mapper.ProfileSpec.DoorOpen), empty when this format describes none. The map
	// pane needs it to TELL the operator what a route will send for an unannotated
	// door — and it has to come from the server, because it is a property of the MUD
	// on the other end, not of the client. A constant in the UI would say "откр две"
	// on an English mud.
	MapperDoorOpen string `json:"mapper_door_open,omitempty"`

	// Server-driven mapper walk state. Sent after every transition and replayed on
	// restore so a page reload cannot hide an in-flight walk's cancel control.
	WalkActive       bool           `json:"walk_active,omitempty"`
	WalkEpoch        int64          `json:"walk_epoch,omitempty"`
	WalkJobID        uint64         `json:"walk_job_id,omitempty"`
	WalkCurrent      int            `json:"walk_current,omitempty"`
	WalkTotal        int            `json:"walk_total,omitempty"`
	WalkReason       string         `json:"walk_reason,omitempty"`
	WalkReasonCode   string         `json:"walk_reason_code,omitempty"`
	WalkReasonArgs   map[string]any `json:"walk_reason_args,omitempty"`
	WalkReasonDetail string         `json:"walk_reason_detail,omitempty"`
	WalkState        string         `json:"walk_state,omitempty"`
	WalkCanResume    bool           `json:"walk_can_resume"`
	WalkRevision     uint64         `json:"walk_revision,omitempty"`

	// Instance-wide UI locale notification. This changes presentation only; it
	// never reloads the VM, parser, tracker, or transport.
	AppLanguage string `json:"app_language,omitempty"`
}

type RoomScanHint struct {
	Dir  string `json:"dir"`
	Hint string `json:"hint"`
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
