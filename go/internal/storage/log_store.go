package storage

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

func (r *LogRecord) TableName() string {
	return "log_entries"
}

func (o *LogOverlay) TableName() string {
	return "log_overlays"
}

func (s *Store) AppendLogEntry(sessionID int64, buffer, rawText, plainText string) (int64, error) {
	return s.AppendLogEntryWithOverlays(sessionID, buffer, rawText, plainText, nil)
}

func (s *Store) AppendLogEntryWithOverlays(sessionID int64, buffer, rawText, plainText string, overlays []LogOverlay) (int64, error) {
	if buffer == "" {
		buffer = "main"
	}
	entry := LogRecord{
		SessionID:  sessionID,
		WindowName: buffer,
		Stream:     "mud",
		RawText:    rawText,
		PlainText:  plainText,
		SourceType: "mud",
		CreatedAt:  nowSQLiteTime(),
		ReceivedAt: nowSQLiteTime(),
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		for _, overlay := range overlays {
			overlay.ID = 0
			overlay.LogEntryID = entry.ID
			if overlay.CreatedAt.Time.IsZero() {
				overlay.CreatedAt = nowSQLiteTime()
			}
			if err := tx.Create(&overlay).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return entry.ID, err
}

const visibleLogEntrySQL = "NOT EXISTS (SELECT 1 FROM log_overlays gag WHERE gag.log_entry_id = log_entries.id AND gag.overlay_type = 'gag')"

func (s *Store) LogEntriesForBuffer(sessionID int64, buffer string, limit int) ([]LogEntry, error) {
	if buffer == "" {
		buffer = "main"
	}
	var records []LogRecord
	err := s.db.Where("session_id = ? AND window_name = ?", sessionID, buffer).
		Where(visibleLogEntrySQL).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, err
	}

	entries := make([]LogEntry, 0, len(records))
	for _, r := range records {
		entries = append(entries, LogEntry{
			ID:        r.ID,
			Buffer:    r.WindowName,
			RawText:   r.RawText,
			PlainText: r.PlainText,
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

func (s *Store) RecentLogs(sessionID int64, limit int) ([]LogEntry, error) {
	return s.LogRangeDetailed(sessionID, 9223372036854775807, limit)
}

func (s *Store) RecentLogsPerBuffer(sessionID int64, limit int) (map[string][]LogEntry, error) {
	var distinctBuffers []string
	if err := s.db.Model(&LogRecord{}).Where("session_id = ?", sessionID).Where(visibleLogEntrySQL).Distinct("window_name").Pluck("window_name", &distinctBuffers).Error; err != nil {
		return nil, err
	}

	result := make(map[string][]LogEntry)
	for _, b := range distinctBuffers {
		entries, err := s.LogEntriesForBuffer(sessionID, b, limit)
		if err != nil {
			return nil, err
		}
		result[b] = entries
	}
	return result, nil
}

// LatestLogID returns the highest log entry ID for the session, or 0 if none.
func (s *Store) LatestLogID(sessionID int64) (int64, error) {
	var id int64
	err := s.db.Model(&LogRecord{}).Where("session_id = ?", sessionID).Order("id DESC").Limit(1).Pluck("id", &id).Error
	return id, err
}

// LogsSinceID returns log entries with id > afterID in chronological order, up to limit.
func (s *Store) LogsSinceID(sessionID, afterID int64, limit int) ([]LogEntry, error) {
	var records []LogRecord
	err := s.db.Where("session_id = ? AND id > ?", sessionID, afterID).
		Where(visibleLogEntrySQL).
		Order("id ASC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	entries := make([]LogEntry, 0, len(records))
	for _, r := range records {
		entries = append(entries, LogEntry{
			ID:        r.ID,
			Buffer:    r.WindowName,
			RawText:   r.RawText,
			PlainText: r.PlainText,
			CreatedAt: r.CreatedAt,
		})
	}
	if err := s.loadOverlays(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) LogRangeDetailed(sessionID, beforeID int64, limit int) ([]LogEntry, error) {
	var records []LogRecord
	err := s.db.Where("session_id = ? AND id < ?", sessionID, beforeID).
		Where(visibleLogEntrySQL).
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
			Buffer:    r.WindowName,
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

func (s *Store) AppendCommandOverlay(entryID int64, command string) error {
	command = strings.TrimSpace(command)
	if command == "" || entryID == 0 {
		return nil
	}

	payload, _ := json.Marshal(commandOverlayPayload{Command: command})

	return s.db.Create(&LogOverlay{
		LogEntryID:  entryID,
		OverlayType: "command",
		PayloadJSON: string(payload),
	}).Error
}

func (s *Store) AppendCommandHintToLatestLogEntry(sessionID int64, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	var lastEntry LogRecord
	err := s.db.Where("session_id = ?", sessionID).Where(visibleLogEntrySQL).Order("created_at DESC, id DESC").First(&lastEntry).Error
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
	err := s.db.Where("overlay_type IN ('command_hint', 'button', 'substitution', 'gag') AND log_entry_id IN ?", ids).
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
		entries[index].Overlays = append(entries[index].Overlays, o)

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

	for i := range entries {
		displayRaw, displayPlain, hidden := ReplayAppliedOverlays(entries[i].RawText, entries[i].PlainText, entries[i].Overlays)
		entries[i].DisplayRaw = displayRaw
		entries[i].DisplayPlain = displayPlain
		entries[i].Hidden = hidden
	}

	return nil
}

func (s *Store) SearchLogsDetailed(sessionID int64, query string, contextLines int, beforeID int64) ([][]LogEntry, error) {
	like := "%" + query + "%"

	// Match log entries by MUD output text.
	var textMatchIDs []int64
	db := s.db.Model(&LogRecord{}).Where("session_id = ? AND plain_text LIKE ?", sessionID, like).Where(visibleLogEntrySQL)
	if beforeID > 0 {
		db = db.Where("id < ?", beforeID)
	}
	if err := db.Order("id DESC").Limit(200).Pluck("id", &textMatchIDs).Error; err != nil {
		return nil, err
	}

	// Also match log entries where a sent command (command_hint overlay) contains the query.
	var cmdMatchIDs []int64
	cmdDB := s.db.Model(&LogOverlay{}).
		Where("overlay_type = 'command_hint' AND payload_json LIKE ?", like).
		Where("log_entry_id IN (SELECT id FROM log_entries WHERE session_id = ? AND "+visibleLogEntrySQL+")", sessionID)
	if beforeID > 0 {
		cmdDB = cmdDB.Where("log_entry_id < ?", beforeID)
	}
	if err := cmdDB.Order("log_entry_id DESC").Limit(200).Pluck("log_entry_id", &cmdMatchIDs).Error; err != nil {
		return nil, err
	}

	// Merge and deduplicate match IDs.
	seenMatch := make(map[int64]bool)
	var matchingIDs []int64
	for _, id := range append(textMatchIDs, cmdMatchIDs...) {
		if !seenMatch[id] {
			seenMatch[id] = true
			matchingIDs = append(matchingIDs, id)
		}
	}
	if len(matchingIDs) == 0 {
		return nil, nil
	}

	allIDsMap := make(map[int64]bool)
	for _, mid := range matchingIDs {
		allIDsMap[mid] = true
		var beforeIDs []int64
		s.db.Model(&LogRecord{}).Where("session_id = ? AND id < ?", sessionID, mid).Where(visibleLogEntrySQL).Order("id DESC").Limit(contextLines).Pluck("id", &beforeIDs)
		for _, id := range beforeIDs {
			allIDsMap[id] = true
		}
		var afterIDs []int64
		s.db.Model(&LogRecord{}).Where("session_id = ? AND id > ?", sessionID, mid).Where(visibleLogEntrySQL).Order("id ASC").Limit(contextLines).Pluck("id", &afterIDs)
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
	if err := s.db.Where("id IN ?", allIDs).Where(visibleLogEntrySQL).Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}

	var allEntries []LogEntry
	for _, r := range records {
		allEntries = append(allEntries, LogEntry{
			ID:        r.ID,
			Buffer:    r.WindowName,
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
			s.db.Model(&LogRecord{}).Where("session_id = ? AND id > ? AND id < ?", sessionID, records[i-1].ID, records[i].ID).Where(visibleLogEntrySQL).Count(&count)
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

type substitutionOverlayPayload struct {
	ReplacementRaw   string `json:"replacement_raw"`
	ReplacementPlain string `json:"replacement_plain"`
	RuleID           int64  `json:"rule_id"`
	PatternTemplate  string `json:"pattern_template"`
	EffectivePattern string `json:"effective_pattern"`
}

func ReplayAppliedOverlays(rawText, plainText string, overlays []LogOverlay) (string, string, bool) {
	for _, overlay := range overlays {
		if overlay.OverlayType == "gag" {
			return rawText, plainText, true
		}
	}

	displayRaw := rawText
	displayPlain := plainText
	subs := make([]LogOverlay, 0, len(overlays))
	for _, overlay := range overlays {
		if overlay.OverlayType == "substitution" {
			subs = append(subs, overlay)
		}
	}
	sort.SliceStable(subs, func(i, j int) bool {
		if subs[i].Layer == subs[j].Layer {
			return subs[i].ID < subs[j].ID
		}
		return subs[i].Layer < subs[j].Layer
	})

	for _, overlay := range subs {
		if overlay.StartOffset == nil || overlay.EndOffset == nil || *overlay.StartOffset == *overlay.EndOffset {
			continue
		}
		if *overlay.StartOffset < 0 || *overlay.EndOffset > len(displayPlain) || *overlay.StartOffset > *overlay.EndOffset {
			continue
		}
		var payload substitutionOverlayPayload
		if err := json.Unmarshal([]byte(overlay.PayloadJSON), &payload); err != nil {
			continue
		}
		rawStart, rawEnd, ok := plainRangeToRawRange(displayRaw, *overlay.StartOffset, *overlay.EndOffset)
		if !ok || rawStart < 0 || rawEnd > len(displayRaw) || rawStart > rawEnd {
			continue
		}
		displayRaw = displayRaw[:rawStart] + payload.ReplacementRaw + displayRaw[rawEnd:]
		displayPlain = displayPlain[:*overlay.StartOffset] + payload.ReplacementPlain + displayPlain[*overlay.EndOffset:]
	}

	return displayRaw, displayPlain, false
}

func plainRangeToRawRange(raw string, startPlain, endPlain int) (int, int, bool) {
	plainOffset := 0
	rawStart := -1
	rawEnd := -1
	inEscape := false

	for i := 0; i < len(raw); {
		if !inEscape && raw[i] == 0x1b {
			inEscape = true
			i++
			continue
		}
		if inEscape {
			if (raw[i] >= 'a' && raw[i] <= 'z') || (raw[i] >= 'A' && raw[i] <= 'Z') {
				inEscape = false
			}
			i++
			continue
		}
		if plainOffset == startPlain && rawStart == -1 {
			rawStart = i
		}
		_, size := utf8.DecodeRuneInString(raw[i:])
		if size <= 0 {
			return 0, 0, false
		}
		i += size
		plainOffset += size
		if plainOffset == endPlain {
			rawEnd = i
			break
		}
	}

	if startPlain == endPlain || rawStart == -1 || rawEnd == -1 {
		return 0, 0, false
	}
	return rawStart, rawEnd, true
}
