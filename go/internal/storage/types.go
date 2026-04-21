package storage

import "database/sql"

type Store struct {
	db *sql.DB
}

type SessionRecord struct {
	ID      int64
	Name    string
	MudHost string
	MudPort int
	Status  string
}

type ButtonOverlay struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type LogEntry struct {
	ID       int64
	RawText  string
	Commands []string
	Buttons  []ButtonOverlay
}

type Variable struct {
	Key   string
	Value string
}

type commandOverlayPayload struct {
	Command string `json:"command"`
}
