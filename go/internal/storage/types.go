package storage

import "gorm.io/gorm"

type Store struct {
	db *gorm.DB
}

func NewTestStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying gorm.DB, intended for use in tests only.
func (s *Store) DB() *gorm.DB {
	return s.db
}

type AppSetting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `json:"value"`
}

type SessionRecord struct {
	ID                    int64       `gorm:"primaryKey" json:"id"`
	Name                  string      `json:"name"`
	MudHost               string      `json:"mud_host"`
	MudPort               int         `json:"mud_port"`
	Status                string      `json:"status"`
	InitialCommands       string      `json:"initial_commands"`
	MCCPEnabled           int         `gorm:"default:1" json:"mccp_enabled"`
	MCCPActive            bool        `gorm:"-" json:"mccp_active"`
	MCCPCompressedBytes   uint64      `gorm:"-" json:"mccp_compressed_bytes"`
	MCCPDecompressedBytes uint64      `gorm:"-" json:"mccp_decompressed_bytes"`
	MCCPCompressionRatio  string      `gorm:"-" json:"mccp_compression_ratio"`
	LastConnectedAt       *SQLiteTime `json:"last_connected_at"`
	LastDisconnectedAt    *SQLiteTime `json:"last_disconnected_at"`
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
	SessionID  int64      `gorm:"index" json:"session_id"`
	Stream     string     `json:"stream"`
	WindowName string     `gorm:"index" json:"window_name"`
	RawText    string     `json:"raw_text"`
	PlainText  string     `json:"plain_text"`
	RawBytes   []byte     `json:"raw_bytes"`
	Encoding   string     `json:"encoding"`
	SourceType string     `json:"source_type"`
	SourceID   string     `json:"source_id"`
	ReceivedAt SQLiteTime `json:"received_at"`
	CreatedAt  SQLiteTime `gorm:"index" json:"created_at"`
}

type LogOverlay struct {
	ID          int64      `gorm:"primaryKey"`
	LogEntryID  int64      `gorm:"index" json:"log_entry_id"`
	OverlayType string     `gorm:"index" json:"overlay_type"`
	Layer       int        `json:"layer"`
	StartOffset *int       `json:"start_offset"`
	EndOffset   *int       `json:"end_offset"`
	PayloadJSON string     `json:"payload_json"`
	SourceType  string     `json:"source_type"`
	SourceID    string     `json:"source_id"`
	CreatedAt   SQLiteTime `json:"created_at"`
}

type ButtonOverlay struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type LogEntry struct {
	ID           int64
	Buffer       string
	RawText      string
	PlainText    string
	DisplayRaw   string
	DisplayPlain string
	Hidden       bool
	CreatedAt    SQLiteTime
	Commands     []string
	Buttons      []ButtonOverlay
	Overlays     []LogOverlay
}

func (e LogEntry) DisplayRawText() string {
	if e.DisplayRaw != "" || e.DisplayPlain != "" || len(e.Overlays) > 0 {
		return e.DisplayRaw
	}
	return e.RawText
}

func (e LogEntry) DisplayPlainText() string {
	if e.DisplayRaw != "" || e.DisplayPlain != "" || len(e.Overlays) > 0 {
		return e.DisplayPlain
	}
	return e.PlainText
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
	ID          int64  `gorm:"primaryKey" json:"id"`
	ProfileID   int64  `json:"profile_id"`
	Position    int    `json:"position"`
	Shortcut    string `json:"shortcut"`
	Command     string `json:"command"`
	MobileRow   int    `gorm:"column:mobile_row" json:"mobile_row"`
	MobileOrder int    `gorm:"column:mobile_order" json:"mobile_order"`
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

type TimerRecord struct {
	SessionID   int64       `gorm:"primaryKey"`
	Name        string      `gorm:"primaryKey"`
	CycleMS     int         `gorm:"column:cycle_ms"`
	NextTickAt  *SQLiteTime `gorm:"column:next_tick_at"`
	RemainingMS int         `gorm:"column:remaining_ms"`
	Enabled     bool        `gorm:"column:enabled"`
	Icon        string      `gorm:"column:icon"`
	RepeatMode  string      `gorm:"column:repeat_mode" json:"repeat_mode"`
}

func (TimerRecord) TableName() string {
	return "timers"
}

type TimerSubscriptionRecord struct {
	SessionID int64  `gorm:"primaryKey"`
	TimerName string `gorm:"primaryKey"`
	Second    int    `gorm:"primaryKey"`
	SortOrder int    `gorm:"primaryKey"`
	Command   string `gorm:"column:command"`
}

func (TimerSubscriptionRecord) TableName() string {
	return "timer_subscriptions"
}

type ProfileTimer struct {
	ProfileID  int64  `gorm:"primaryKey" json:"profile_id"`
	Name       string `gorm:"primaryKey" json:"name"`
	Icon       string `json:"icon"`
	CycleMS    int    `gorm:"column:cycle_ms" json:"cycle_ms"`
	RepeatMode string `gorm:"column:repeat_mode" json:"repeat_mode"`
}

func (ProfileTimer) TableName() string {
	return "profile_timers"
}

type ProfileTimerSubscription struct {
	ProfileID int64  `gorm:"primaryKey" json:"profile_id"`
	TimerName string `gorm:"primaryKey" json:"timer_name"`
	Second    int    `gorm:"primaryKey" json:"second"`
	SortOrder int    `gorm:"primaryKey" json:"sort_order"`
	Command   string `json:"command"`
}

func (ProfileTimerSubscription) TableName() string {
	return "profile_timer_subscriptions"
}
