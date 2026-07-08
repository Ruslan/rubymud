package storage

import (
	"log"
	"time"

	"gorm.io/gorm"
)

func (s *SessionRecord) TableName() string {
	return "sessions"
}

func NormalizeAnsiTheme(theme string) string {
	switch theme {
	case "high-contrast", "tango-dark", "dracula", "gruvbox-dark":
		return theme
	default:
		return "classic"
	}
}

// NormalizeTimezone returns a concrete IANA zone name, defaulting to "UTC" when
// empty or unparseable. A non-empty zone that fails to load is logged: that only
// happens when the tz database is unavailable (e.g. a runtime image with no
// tzdata) and silently swallowing it makes a user's saved zone vanish to UTC
// with no trace — see the `time/tzdata` embed in cmd/mudhost.
func NormalizeTimezone(name string) string {
	if name == "" {
		return "UTC"
	}
	if _, err := time.LoadLocation(name); err != nil {
		log.Printf("NormalizeTimezone: cannot load zone %q, falling back to UTC (missing tzdata?): %v", name, err)
		return "UTC"
	}
	return name
}

// LoadLocationOrUTC resolves an IANA zone name to a *time.Location, falling back
// to time.UTC when empty or unparseable.
func LoadLocationOrUTC(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

func normalizeSessionRecord(record *SessionRecord) {
	record.AnsiTheme = NormalizeAnsiTheme(record.AnsiTheme)
	record.Timezone = NormalizeTimezone(record.Timezone)
}

func (s *Store) EnsureDefaultSession(host string, port int) (SessionRecord, error) {
	var record SessionRecord
	err := s.db.Where("name = ?", "default").Order("id ASC").First(&record).Error

	now := nowSQLiteTimePtr()
	if err == gorm.ErrRecordNotFound {
		record = SessionRecord{
			Name:            "default",
			MudHost:         host,
			MudPort:         port,
			Status:          "disconnected",
			AnsiTheme:       "classic",
			Timezone:        "UTC",
			TZFollow:        1,
			MCCPEnabled:     1,
			LastConnectedAt: now,
		}
		if err := s.db.Create(&record).Error; err != nil {
			return SessionRecord{}, err
		}
		if err := s.EnsureSessionProfiles(record.ID, record.Name); err != nil {
			return SessionRecord{}, err
		}
		return record, nil
	}
	if err != nil {
		return SessionRecord{}, err
	}
	if err := s.EnsureSessionProfiles(record.ID, record.Name); err != nil {
		return SessionRecord{}, err
	}

	normalizeSessionRecord(&record)
	return record, nil
}

func (s *Store) GetSession(id int64) (SessionRecord, error) {
	var record SessionRecord
	err := s.db.First(&record, id).Error
	normalizeSessionRecord(&record)
	return record, err
}

func (s *Store) CreateSession(name, host string, port int) (SessionRecord, error) {
	record := SessionRecord{
		Name:        name,
		MudHost:     host,
		MudPort:     port,
		Status:      "disconnected",
		AnsiTheme:   "classic",
		Timezone:    "UTC",
		TZFollow:    1,
		MCCPEnabled: 1,
	}
	err := s.db.Create(&record).Error
	if err != nil {
		return record, err
	}
	err = s.EnsureSessionProfiles(record.ID, record.Name)
	return record, err
}

func (s *Store) UpdateSession(record SessionRecord) error {
	normalizeSessionRecord(&record)
	return s.db.Save(&record).Error
}

func (s *Store) DeleteSession(id int64) error {
	return s.db.Delete(&SessionRecord{}, id).Error
}

func (s *Store) ListSessions() ([]SessionRecord, error) {
	var sessions []SessionRecord
	err := s.db.Order("id ASC").Find(&sessions).Error
	for i := range sessions {
		normalizeSessionRecord(&sessions[i])
	}
	return sessions, err
}

// UpdateSessionTimezone persists only the timezone column (normalized), leaving
// other columns untouched so concurrent status/connection updates are not
// clobbered.
func (s *Store) UpdateSessionTimezone(sessionID int64, tz string) error {
	return s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).
		Update("timezone", NormalizeTimezone(tz)).Error
}

func (s *Store) MarkSessionConnected(sessionID int64) error {
	now := nowSQLiteTimePtr()
	return s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).Updates(map[string]any{
		"status":            "connected",
		"last_connected_at": now,
	}).Error
}

func (s *Store) MarkSessionDisconnected(sessionID int64) error {
	now := nowSQLiteTimePtr()
	return s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).Updates(map[string]any{
		"status":               "disconnected",
		"last_disconnected_at": now,
	}).Error
}
