package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) AppendLogEntry(sessionID int64, rawText, plainText string) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO log_entries(session_id, stream, window_name, raw_text, plain_text, source_type)
		VALUES(?, 'mud', NULL, ?, ?, 'mud')
	`, sessionID, rawText, plainText)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
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
