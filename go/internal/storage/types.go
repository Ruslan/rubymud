package storage

import "gorm.io/gorm"

type Store struct {
	db *gorm.DB
}

func NewTestStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

type AppSetting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `json:"value"`
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
	ID        int64
	RawText   string
	PlainText string
	CreatedAt SQLiteTime
	Commands  []string
	Buttons   []ButtonOverlay
}

type commandOverlayPayload struct {
	Command string `json:"command"`
}

type Profile struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	CreatedAt   SQLiteTime `json:"created_at"`
}

type SessionProfile struct {
	ID         int64 `gorm:"primaryKey"`
	SessionID  int64 `gorm:"uniqueIndex:idx_session_profile" json:"session_id"`
	ProfileID  int64 `gorm:"uniqueIndex:idx_session_profile" json:"profile_id"`
	OrderIndex int   `json:"order_index"`
}

type HotkeyRule struct {
	ID        int64  `gorm:"primaryKey" json:"id"`
	ProfileID int64  `json:"profile_id"`
	Position  int    `json:"position"`
	Shortcut  string `json:"shortcut"`
	Command   string `json:"command"`
}

type ProfileVariable struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	ProfileID    int64      `gorm:"uniqueIndex:idx_profile_var" json:"profile_id"`
	Position     int        `json:"position"`
	Name         string     `gorm:"uniqueIndex:idx_profile_var" json:"name"`
	DefaultValue string     `json:"default_value"`
	Description  string     `json:"description"`
	UpdatedAt    SQLiteTime `json:"updated_at"`
}


