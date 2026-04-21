package storage

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

func (r *LogRecord) TableName() string {
	return "log_entries"
}

func (o *LogOverlay) TableName() string {
	return "log_overlays"
}

func (s *Store) AppendLogEntry(sessionID int64, rawText, plainText string) (int64, error) {
	entry := LogRecord{
		SessionID:  sessionID,
		Stream:     "mud",
		RawText:    rawText,
		PlainText:  plainText,
		SourceType: "mud",
		CreatedAt:  nowSQLiteTime(),
	}
	err := s.db.Create(&entry).Error
	return entry.ID, err
}

func (s *Store) RecentLogs(sessionID int64, limit int) ([]LogEntry, error) {
	var records []LogRecord
	err := s.db.Where("session_id = ?", sessionID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, err
	}

	entries := make([]LogEntry, 0, len(records))
	for _, r := range records {
		entries = append(entries, LogEntry{
			ID:      r.ID,
			RawText: r.RawText,
		})
	}

	// Reverse to get chronological order (oldest first)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

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

	var lastEntry LogRecord
	err := s.db.Where("session_id = ?", sessionID).Order("created_at DESC, id DESC").First(&lastEntry).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	payload, err := json.Marshal(commandOverlayPayload{Command: command})
	if err != nil {
		return err
	}

	overlay := LogOverlay{
		LogEntryID:  lastEntry.ID,
		OverlayType: "command_hint",
		PayloadJSON: string(payload),
		SourceType:  "client",
		CreatedAt:   nowSQLiteTime(),
	}
	return s.db.Create(&overlay).Error
}

func (s *Store) AppendButtonOverlay(logEntryID int64, label, command string) error {
	payload, err := json.Marshal(map[string]string{"label": label, "command": command})
	if err != nil {
		return err
	}
	overlay := LogOverlay{
		LogEntryID:  logEntryID,
		OverlayType: "button",
		PayloadJSON: string(payload),
		SourceType:  "trigger",
		CreatedAt:   nowSQLiteTime(),
	}
	return s.db.Create(&overlay).Error
}

func (s *Store) loadOverlays(entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	ids := make([]int64, len(entries))
	idIndex := make(map[int64]int, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
		idIndex[entry.ID] = i
	}

	var overlays []LogOverlay
	err := s.db.Where("overlay_type IN ('command_hint', 'button') AND log_entry_id IN ?", ids).
		Order("id ASC").
		Find(&overlays).Error
	if err != nil {
		return err
	}

	for _, o := range overlays {
		index, ok := idIndex[o.LogEntryID]
		if !ok {
			continue
		}

		switch o.OverlayType {
		case "command_hint":
			var payload commandOverlayPayload
			if err := json.Unmarshal([]byte(o.PayloadJSON), &payload); err != nil {
				return err
			}
			if payload.Command != "" {
				entries[index].Commands = append(entries[index].Commands, payload.Command)
			}
		case "button":
			var payload ButtonOverlay
			if err := json.Unmarshal([]byte(o.PayloadJSON), &payload); err != nil {
				return err
			}
			if payload.Command != "" {
				entries[index].Buttons = append(entries[index].Buttons, payload)
			}
		}
	}

	return nil
}
