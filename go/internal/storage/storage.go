package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

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

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", filepath.Clean(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) EnsureDefaultSession(host string, port int) (SessionRecord, error) {
	const query = `
		SELECT id, name, mud_host, mud_port, status
		FROM sessions
		WHERE name = 'default'
		ORDER BY id ASC
		LIMIT 1
	`

	var record SessionRecord
	err := s.db.QueryRow(query).Scan(&record.ID, &record.Name, &record.MudHost, &record.MudPort, &record.Status)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := s.db.Exec(`
			INSERT INTO sessions(name, mud_host, mud_port, status)
			VALUES('default', ?, ?, 'connected')
		`, host, port)
		if insertErr != nil {
			return SessionRecord{}, insertErr
		}

		id, idErr := result.LastInsertId()
		if idErr != nil {
			return SessionRecord{}, idErr
		}

		return SessionRecord{
			ID:      id,
			Name:    "default",
			MudHost: host,
			MudPort: port,
			Status:  "connected",
		}, nil
	}
	if err != nil {
		return SessionRecord{}, err
	}

	_, err = s.db.Exec(`
		UPDATE sessions
		SET mud_host = ?, mud_port = ?, status = 'connected', updated_at = CURRENT_TIMESTAMP, last_connected_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, host, port, record.ID)
	if err != nil {
		return SessionRecord{}, err
	}

	record.MudHost = host
	record.MudPort = port
	record.Status = "connected"
	return record, nil
}

func (s *Store) MarkSessionDisconnected(sessionID int64) error {
	_, err := s.db.Exec(`
		UPDATE sessions
		SET status = 'disconnected', updated_at = CURRENT_TIMESTAMP, last_disconnected_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, sessionID)
	return err
}

func (s *Store) AppendLogEntry(sessionID int64, rawText, plainText string) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO log_entries(session_id, stream, window_name, raw_text, plain_text, source_type)
		VALUES(?, 'mud', NULL, ?, ?, 'mud')
	`, sessionID, rawText, plainText)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, nil
}

func (s *Store) RecentLogs(sessionID int64, limit int) ([]LogEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, raw_text
		FROM log_entries
		WHERE session_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]LogEntry, 0, limit)
	for rows.Next() {
		var entry LogEntry
		if err := rows.Scan(&entry.ID, &entry.RawText); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	reverseLogs(entries)
	if err := s.loadOverlays(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) AppendCommandHintToLatestLogEntry(sessionID int64, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	var logEntryID int64
	err := s.db.QueryRow(`
		SELECT id
		FROM log_entries
		WHERE session_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID).Scan(&logEntryID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	payload, err := json.Marshal(commandOverlayPayload{Command: command})
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO log_overlays(log_entry_id, overlay_type, payload_json, source_type)
		VALUES(?, 'command_hint', ?, 'client')
	`, logEntryID, string(payload))
	return err
}

func (s *Store) AppendHistoryEntry(sessionID int64, kind, line string) error {
	_, err := s.db.Exec(`
		INSERT INTO history_entries(session_id, kind, line)
		VALUES(?, ?, ?)
	`, sessionID, kind, strings.TrimSpace(line))
	return err
}

func (s *Store) RecentInputHistory(sessionID int64, limit int) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT line
		FROM history_entries
		WHERE session_id = ? AND kind = 'input'
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		history = append(history, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	return history, nil
}

func (s *Store) AppendButtonOverlay(logEntryID int64, label, command string) error {
	payload, err := json.Marshal(map[string]string{"label": label, "command": command})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO log_overlays(log_entry_id, overlay_type, payload_json, source_type)
		VALUES(?, 'button', ?, 'trigger')
	`, logEntryID, string(payload))
	return err
}

func reverseLogs(entries []LogEntry) {
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
}

func (s *Store) loadOverlays(entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	idIndex := make(map[int64]int, len(entries))
	placeholders := make([]string, 0, len(entries))
	args := make([]any, 0, len(entries))
	for i, entry := range entries {
		idIndex[entry.ID] = i
		placeholders = append(placeholders, "?")
		args = append(args, entry.ID)
	}

	query := fmt.Sprintf(`
		SELECT log_entry_id, overlay_type, payload_json
		FROM log_overlays
		WHERE overlay_type IN ('command_hint', 'button') AND log_entry_id IN (%s)
		ORDER BY id ASC
	`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var logEntryID int64
		var overlayType string
		var payloadText string
		if err := rows.Scan(&logEntryID, &overlayType, &payloadText); err != nil {
			return err
		}

		index, ok := idIndex[logEntryID]
		if !ok {
			continue
		}

		switch overlayType {
		case "command_hint":
			var payload commandOverlayPayload
			if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
				return err
			}
			if payload.Command != "" {
				entries[index].Commands = append(entries[index].Commands, payload.Command)
			}
		case "button":
			var payload ButtonOverlay
			if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
				return err
			}
			if payload.Command != "" {
				entries[index].Buttons = append(entries[index].Buttons, payload)
			}
		}
	}

	return rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
