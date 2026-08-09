package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const webAuthSettingKey = "web_auth"

// MCPSettings is the persistent, singleton-owned MCP configuration. TokenDigest
// is a SHA-256 hexadecimal digest, never the bearer credential itself.
type MCPSettings struct {
	Enabled     bool
	TokenDigest string
	Revision    uint64
	UpdatedAt   time.Time
}

// WebAuthSettings is the database-owned browser access gate. PasswordHash is
// an adaptive password hash; plaintext credentials must never cross this
// storage boundary.
type WebAuthSettings struct {
	Enabled      bool   `json:"enabled"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	// ListenAddr is the database-owned listener for the next process start.
	// An empty legacy value is resolved to the local-only default by web.
	ListenAddr string    `json:"listen_addr"`
	Revision   uint64    `json:"revision"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) GetSetting(key string) (string, error) {
	var setting AppSetting
	err := s.db.Where("key = ?", key).First(&setting).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return setting.Value, err
}

func (s *Store) SetSetting(key, value string) error {
	setting := AppSetting{Key: key, Value: value}
	return s.db.Save(&setting).Error
}

func (s *Store) GetWebAuthSettings() (WebAuthSettings, error) {
	return getWebAuthSettings(s.db)
}

func getWebAuthSettings(db *gorm.DB) (WebAuthSettings, error) {
	var setting AppSetting
	err := db.Where("key = ?", webAuthSettingKey).Take(&setting).Error
	if err == gorm.ErrRecordNotFound {
		return WebAuthSettings{}, nil
	}
	if err != nil {
		return WebAuthSettings{}, err
	}
	var settings WebAuthSettings
	if err := json.Unmarshal([]byte(setting.Value), &settings); err != nil {
		return WebAuthSettings{}, fmt.Errorf("decode web auth settings: %w", err)
	}
	return settings, nil
}

// SaveWebAuthSettings replaces the gate and credential together, then
// publishes the committed revision to every server backed by this Store.
func (s *Store) SaveWebAuthSettings(settings WebAuthSettings) (WebAuthSettings, error) {
	s.webAuthUpdateMu.Lock()
	defer s.webAuthUpdateMu.Unlock()

	var saved WebAuthSettings
	err := s.db.Transaction(func(tx *gorm.DB) error {
		current, err := getWebAuthSettings(tx)
		if err != nil {
			return err
		}
		settings.Revision = current.Revision + 1
		settings.UpdatedAt = time.Now().UTC()
		encoded, err := json.Marshal(settings)
		if err != nil {
			return fmt.Errorf("encode web auth settings: %w", err)
		}
		if err := tx.Save(&AppSetting{Key: webAuthSettingKey, Value: string(encoded)}).Error; err != nil {
			return err
		}
		saved, err = getWebAuthSettings(tx)
		return err
	})
	if err != nil {
		return WebAuthSettings{}, err
	}
	s.publishWebAuthSettings(saved)
	return saved, nil
}

func (s *Store) SubscribeWebAuthSettings(notify func(WebAuthSettings)) func() {
	listener := &webAuthSettingsListener{active: true, notify: notify}
	s.webAuthListenersMu.Lock()
	if s.webAuthListeners == nil {
		s.webAuthListeners = make(map[*webAuthSettingsListener]struct{})
	}
	s.webAuthListeners[listener] = struct{}{}
	s.webAuthListenersMu.Unlock()
	return func() {
		s.webAuthListenersMu.Lock()
		delete(s.webAuthListeners, listener)
		s.webAuthListenersMu.Unlock()
		listener.mu.Lock()
		listener.active = false
		listener.mu.Unlock()
	}
}

func (s *Store) publishWebAuthSettings(settings WebAuthSettings) {
	s.webAuthListenersMu.Lock()
	listeners := make([]*webAuthSettingsListener, 0, len(s.webAuthListeners))
	for listener := range s.webAuthListeners {
		listeners = append(listeners, listener)
	}
	s.webAuthListenersMu.Unlock()
	for _, listener := range listeners {
		listener.mu.Lock()
		if listener.active {
			listener.notify(settings)
		}
		listener.mu.Unlock()
	}
}

func (s *Store) GetMCPSettings() (MCPSettings, error) {
	return getMCPSettings(s.db)
}

func getMCPSettings(db *gorm.DB) (MCPSettings, error) {
	var row struct {
		Enabled     int        `gorm:"column:enabled"`
		TokenDigest string     `gorm:"column:token_digest"`
		Revision    uint64     `gorm:"column:revision"`
		UpdatedAt   SQLiteTime `gorm:"column:updated_at"`
	}
	err := db.Table("mcp_settings").Where("singleton = 1").Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return MCPSettings{}, fmt.Errorf("MCP settings singleton is missing")
	}
	if err != nil {
		return MCPSettings{}, err
	}
	return MCPSettings{Enabled: row.Enabled != 0, TokenDigest: row.TokenDigest, Revision: row.Revision, UpdatedAt: row.UpdatedAt.Time}, nil
}

// SaveMCPSettings writes enabled state and digest in one SQLite statement. The
// caller updates the in-memory gate only after this transaction has succeeded.
func (s *Store) SaveMCPSettings(settings MCPSettings) error {
	var saved MCPSettings
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(`UPDATE mcp_settings
			SET enabled = ?, token_digest = ?, revision = revision + 1, updated_at = ?
			WHERE singleton = 1`, boolToInt(settings.Enabled), settings.TokenDigest, time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("MCP settings singleton is missing")
		}
		var err error
		saved, err = getMCPSettings(tx)
		return err
	})
	if err == nil {
		s.publishMCPSettings(saved)
	}
	return err
}

// UpdateMCPEnabled changes only enabled state and atomically fills an absent
// digest. The returned boolean says whether digestIfMissing became the stored
// credential, so callers never disclose a losing concurrent candidate.
func (s *Store) UpdateMCPEnabled(enabled bool, digestIfMissing string) (MCPSettings, bool, error) {
	var saved MCPSettings
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(`UPDATE mcp_settings
			SET enabled = ?,
				token_digest = CASE WHEN token_digest = '' THEN ? ELSE token_digest END,
				revision = revision + 1,
				updated_at = ?
			WHERE singleton = 1`, boolToInt(enabled), digestIfMissing, time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("MCP settings singleton is missing")
		}
		var err error
		saved, err = getMCPSettings(tx)
		return err
	})
	if err == nil {
		s.publishMCPSettings(saved)
	}
	return saved, digestIfMissing != "" && saved.TokenDigest == digestIfMissing, err
}

// RotateMCPTokenDigest changes only the credential, retaining enabled state.
func (s *Store) RotateMCPTokenDigest(digest string) (MCPSettings, error) {
	var saved MCPSettings
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(`UPDATE mcp_settings SET token_digest = ?, revision = revision + 1, updated_at = ? WHERE singleton = 1`, digest, time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("MCP settings singleton is missing")
		}
		var err error
		saved, err = getMCPSettings(tx)
		return err
	})
	if err == nil {
		s.publishMCPSettings(saved)
	}
	return saved, err
}

// SubscribeMCPSettings keeps all Server instances backed by this Store on the
// same post-commit runtime state. Notifications never run inside a DB
// transaction, and unsubscribe waits for any callback already in progress.
func (s *Store) SubscribeMCPSettings(notify func(MCPSettings)) func() {
	listener := &mcpSettingsListener{active: true, notify: notify}
	s.mcpListenersMu.Lock()
	if s.mcpListeners == nil {
		s.mcpListeners = make(map[*mcpSettingsListener]struct{})
	}
	s.mcpListeners[listener] = struct{}{}
	s.mcpListenersMu.Unlock()
	return func() {
		s.mcpListenersMu.Lock()
		delete(s.mcpListeners, listener)
		s.mcpListenersMu.Unlock()
		listener.mu.Lock()
		listener.active = false
		listener.mu.Unlock()
	}
}

func (s *Store) publishMCPSettings(settings MCPSettings) {
	s.mcpListenersMu.Lock()
	listeners := make([]*mcpSettingsListener, 0, len(s.mcpListeners))
	for listener := range s.mcpListeners {
		listeners = append(listeners, listener)
	}
	s.mcpListenersMu.Unlock()
	for _, listener := range listeners {
		listener.mu.Lock()
		if listener.active {
			listener.notify(settings)
		}
		listener.mu.Unlock()
	}
}
