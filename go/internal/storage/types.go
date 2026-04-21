package storage

import "gorm.io/gorm"

type Store struct {
	db *gorm.DB
}

func NewTestStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

type SessionRecord struct {
	ID                 int64       `gorm:"primaryKey" json:"id"`
	Name               string      `json:"name"`
	MudHost            string      `json:"mud_host"`
	MudPort            int         `json:"mud_port"`
	Status             string      `json:"status"`
	LastConnectedAt    *SQLiteTime `json:"last_connected_at"`
	LastDisconnectedAt *SQLiteTime `json:"last_disconnected_at"`
}

type HistoryEntry struct {
	ID        int64      `gorm:"primaryKey"`
	SessionID int64      `json:"session_id"`
	Kind      string     `json:"kind"`
	Line      string     `json:"line"`
	CreatedAt SQLiteTime `json:"created_at"`
}

type LogRecord struct {
	ID         int64      `gorm:"primaryKey"`
	SessionID  int64      `json:"session_id"`
	Stream     string     `json:"stream"`
	RawText    string     `json:"raw_text"`
	PlainText  string     `json:"plain_text"`
	SourceType string     `json:"source_type"`
	CreatedAt  SQLiteTime `json:"created_at"`
}

type LogOverlay struct {
	ID          int64      `gorm:"primaryKey"`
	LogEntryID  int64      `json:"log_entry_id"`
	OverlayType string     `json:"overlay_type"`
	PayloadJSON string     `json:"payload_json"`
	SourceType  string     `json:"source_type"`
	CreatedAt   SQLiteTime `json:"created_at"`
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

type commandOverlayPayload struct {
	Command string `json:"command"`
}
