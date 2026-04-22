package storage

import (
	"encoding/json"
	"sort"
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
	return s.LogRangeDetailed(sessionID, 9223372036854775807, limit)
}

func (s *Store) LogRangeDetailed(sessionID, beforeID int64, limit int) ([]LogEntry, error) {
	var records []LogRecord
	err := s.db.Where("session_id = ? AND id < ?", sessionID, beforeID).
		Order("id DESC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, err
	}

	entries := make([]LogEntry, 0, len(records))
	for _, r := range records {
		entries = append(entries, LogEntry{
			ID:        r.ID,
			RawText:   r.RawText,
			PlainText: r.PlainText,
			CreatedAt: r.CreatedAt,
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

func (s *Store) SearchLogsDetailed(sessionID int64, query string, contextLines int, beforeID int64) ([][]LogEntry, error) {
	var matchingIDs []int64
	db := s.db.Model(&LogRecord{}).
		Where("session_id = ? AND plain_text LIKE ?", sessionID, "%"+query+"%")
	if beforeID > 0 {
		db = db.Where("id < ?", beforeID)
	}
	// Limit candidates to avoid building context for hundreds of matches.
	// Ordered DESC so we process the newest matches first.
	err := db.Order("id DESC").Limit(200).Pluck("id", &matchingIDs).Error
	if err != nil || len(matchingIDs) == 0 {
		return nil, err
	}

	allIDsMap := make(map[int64]bool)
	for _, mid := range matchingIDs {
		allIDsMap[mid] = true
		var beforeIDs []int64
		s.db.Model(&LogRecord{}).Where("session_id = ? AND id < ?", sessionID, mid).Order("id DESC").Limit(contextLines).Pluck("id", &beforeIDs)
		for _, id := range beforeIDs {
			allIDsMap[id] = true
		}
		var afterIDs []int64
		s.db.Model(&LogRecord{}).Where("session_id = ? AND id > ?", sessionID, mid).Order("id ASC").Limit(contextLines).Pluck("id", &afterIDs)
		for _, id := range afterIDs {
			allIDsMap[id] = true
		}
	}

	var allIDs []int64
	for id := range allIDsMap {
		allIDs = append(allIDs, id)
	}
	sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })

	var records []LogRecord
	if err := s.db.Where("id IN ?", allIDs).Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}

	var allEntries []LogEntry
	for _, r := range records {
		allEntries = append(allEntries, LogEntry{
			ID:        r.ID,
			RawText:   r.RawText,
			PlainText: r.PlainText,
			CreatedAt: r.CreatedAt,
		})
	}
	if err := s.loadOverlays(allEntries); err != nil {
		return nil, err
	}

	entryMap := make(map[int64]LogEntry)
	for _, e := range allEntries {
		entryMap[e.ID] = e
	}

	var groups [][]LogEntry
	if len(records) == 0 {
		return nil, nil
	}

	currentGroup := []LogEntry{entryMap[records[0].ID]}
	for i := 1; i < len(records); i++ {
		if records[i].ID > records[i-1].ID+1 {
			var count int64
			s.db.Model(&LogRecord{}).Where("session_id = ? AND id > ? AND id < ?", sessionID, records[i-1].ID, records[i].ID).Count(&count)
			if count > 0 {
				groups = append(groups, currentGroup)
				currentGroup = []LogEntry{entryMap[records[i].ID]}
				continue
			}
		}
		currentGroup = append(currentGroup, entryMap[records[i].ID])
	}
	groups = append(groups, currentGroup)

	return groups, nil
}
