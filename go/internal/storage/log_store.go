package storage

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
)

func (r *LogRecord) TableName() string {
	return "log_entries"
}

func (o *LogOverlay) TableName() string {
	return "log_overlays"
}

// AppendLogEntry records a LOCAL client echo line (#showme/#wecho, command
// echo, local command output). It is tagged source_type="echo" so the export
// stream can exclude it — only genuine server output (source_type="mud", via
// AppendLogEntryWithOverlays) is exported.
func (s *Store) AppendLogEntry(sessionID int64, buffer, rawText, plainText string) (int64, error) {
	return s.appendLogEntry(sessionID, buffer, rawText, plainText, "echo", nil)
}

// AppendLogEntryWithOverlays records genuine server network output
// (source_type="mud"), optionally with overlays.
func (s *Store) AppendLogEntryWithOverlays(sessionID int64, buffer, rawText, plainText string, overlays []LogOverlay) (int64, error) {
	return s.appendLogEntry(sessionID, buffer, rawText, plainText, "mud", overlays)
}

func (s *Store) appendLogEntry(sessionID int64, buffer, rawText, plainText, sourceType string, overlays []LogOverlay) (int64, error) {
	if buffer == "" {
		buffer = "main"
	}
	entry := LogRecord{
		SessionID:  sessionID,
		WindowName: buffer,
		Stream:     "mud",
		RawText:    rawText,
		PlainText:  plainText,
		SourceType: sourceType,
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

// LatestVisibleLogID returns the highest visible log entry ID for the session, or 0 if none.
func (s *Store) LatestVisibleLogID(sessionID int64) (int64, error) {
	var id int64
	err := s.db.Model(&LogRecord{}).Where("session_id = ?", sessionID).Where(visibleLogEntrySQL).Order("id DESC").Limit(1).Pluck("id", &id).Error
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

func (s *Store) LogRangeByDate(sessionID int64, from, to SQLiteTime, limit, offset int) ([]LogEntry, int64, error) {
	var count int64
	baseQuery := s.db.Model(&LogRecord{}).
		Where("session_id = ?", sessionID).
		Where(visibleLogEntrySQL)

	if !from.Time.IsZero() {
		baseQuery = baseQuery.Where("created_at >= ?", from)
	}
	if !to.Time.IsZero() {
		baseQuery = baseQuery.Where("created_at <= ?", to)
	}

	if err := baseQuery.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	var records []LogRecord
	err := baseQuery.Order("created_at ASC, id ASC").
		Limit(limit).
		Offset(offset).
		Find(&records).Error
	if err != nil {
		return nil, 0, err
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
		return nil, 0, err
	}
	return entries, count, nil
}

func (s *Store) LogEntryByID(sessionID, entryID int64) (LogEntry, error) {
	var record LogRecord
	err := s.db.Where("id = ? AND session_id = ?", entryID, sessionID).Where(visibleLogEntrySQL).First(&record).Error
	if err != nil {
		return LogEntry{}, err
	}
	entry := LogEntry{
		ID:        record.ID,
		Buffer:    record.WindowName,
		RawText:   record.RawText,
		PlainText: record.PlainText,
		CreatedAt: record.CreatedAt,
	}
	entries := []LogEntry{entry}
	if err := s.loadOverlays(entries); err != nil {
		return LogEntry{}, err
	}
	return entries[0], nil
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

// exportSortKeyExpr normalizes created_at into a fixed-width, lexicographically
// sortable key: the 19-char "YYYY-MM-DDTHH:MM:SS" prefix followed by 9-digit,
// right-padded nanoseconds. This makes ordering strictly chronological even
// though RFC3339Nano trims trailing zeros in the fractional part (which would
// otherwise make a raw TEXT compare non-monotonic). Timestamps are always stored
// as UTC RFC3339Nano ("...Z" / "....fffZ") by nowSQLiteTime / SQLiteTime.Value.
const exportSortKeyExpr = "(substr(created_at,1,19) || (CASE WHEN substr(created_at,20,1)='.' THEN substr(replace(substr(created_at,21),'Z','')||'000000000',1,9) ELSE '000000000' END))"

// ExportStreamItem is one row of the merged, time-ordered export stream: either
// a received MUD output line or a sent player command.
type ExportStreamItem struct {
	Kind      string     `json:"kind"`   // "output" | "command"
	Ansi      string     `json:"ansi"`   // output: raw_text (pre-overlay); command: canonical outgoing text
	Buffer    string     `json:"buffer"` // window_name; commands inherit their anchor entry's buffer
	CreatedAt SQLiteTime `json:"created_at"`
	RowID     int64      `json:"row_id"` // PK within the item's source table; unique per (Kind, RowID)
}

// ExportStreamOptions selects and filters the merged export stream.
type ExportStreamOptions struct {
	SessionID       int64
	Buffer          string // optional; filters window_name
	From            SQLiteTime
	To              SQLiteTime
	IncludeCommands bool
}

// buildExportUnion builds the inner UNION query (server "mud" output + optional
// command_hint commands) with its filter args, used by the streaming
// StreamExportLog.
//
// Only genuine server output (source_type="mud") is exported; local client echo
// (source_type="echo") is excluded. Index-friendly via log_entries_source_idx
// (session_id, source_type, created_at). Caveat: rows written before echo
// tagging landed are all "mud" and can't be retroactively distinguished, so very
// old ranges may still include some echo.
func buildExportUnion(opts ExportStreamOptions) (string, []any) {
	var args []any
	buffer := strings.TrimSpace(opts.Buffer)

	outputSQL := "SELECT 'output' AS kind, raw_text AS ansi, window_name AS buffer, created_at AS created_at, id AS row_id FROM log_entries WHERE session_id = ? AND source_type = 'mud'"
	args = append(args, opts.SessionID)
	if buffer != "" {
		outputSQL += " AND window_name = ?"
		args = append(args, buffer)
	}
	if !opts.From.Time.IsZero() {
		outputSQL += " AND created_at >= ?"
		args = append(args, opts.From)
	}
	if !opts.To.Time.IsZero() {
		outputSQL += " AND created_at <= ?"
		args = append(args, opts.To)
	}

	if !opts.IncludeCommands {
		return outputSQL, args
	}

	// payload_json is decoded in Go (decodeCommandItem) so we avoid depending on
	// the SQLite JSON1 extension.
	cmdSQL := "SELECT 'command' AS kind, o.payload_json AS ansi, le.window_name AS buffer, o.created_at AS created_at, o.id AS row_id FROM log_overlays o JOIN log_entries le ON le.id = o.log_entry_id WHERE o.overlay_type = 'command_hint' AND le.session_id = ?"
	args = append(args, opts.SessionID)
	if buffer != "" {
		cmdSQL += " AND le.window_name = ?"
		args = append(args, buffer)
	}
	if !opts.From.Time.IsZero() {
		cmdSQL += " AND o.created_at >= ?"
		args = append(args, opts.From)
	}
	if !opts.To.Time.IsZero() {
		cmdSQL += " AND o.created_at <= ?"
		args = append(args, opts.To)
	}
	return outputSQL + " UNION ALL " + cmdSQL, args
}

// decodeCommandItem replaces a command item's raw payload_json with its
// canonical command text. Output items are left untouched.
func decodeCommandItem(item *ExportStreamItem) error {
	if item.Kind != "command" {
		return nil
	}
	var payload commandOverlayPayload
	if err := json.Unmarshal([]byte(item.Ansi), &payload); err != nil {
		return err
	}
	item.Ansi = payload.Command
	return nil
}

// StreamExportLog runs the merged export query as a SINGLE ordered cursor and
// invokes fn once per row, ordered by the normalized sort key then (kind,
// row_id) — WITHOUT buffering the whole result set (used by the streaming HTML
// export so arbitrarily large ranges never sit in memory).
//
// Output items carry raw_text — the ORIGINAL server ANSI, BEFORE any
// substitution/highlight/gag overlay. Gagged lines are intentionally INCLUDED
// (the export hides nothing).
//
// Command items are canonical outgoing commands sourced from command_hint
// overlays. We deliberately do NOT use history_entries(kind='expanded'): that
// table is deduplicated by (session_id, line) via an upsert, so repeated
// commands collapse into a single row keeping only the latest timestamp, which
// is unusable for a time-ordered replay. command_hint overlays are appended
// once per send, each with its own created_at, giving a faithful stream.
func (s *Store) StreamExportLog(opts ExportStreamOptions, fn func(ExportStreamItem) error) error {
	union, args := buildExportUnion(opts)
	query := "SELECT kind, ansi, buffer, created_at, row_id FROM (SELECT kind, ansi, buffer, created_at, row_id, " + exportSortKeyExpr + " AS sort_key FROM (" + union + ") AS merged) AS keyed ORDER BY sort_key ASC, kind ASC, row_id ASC"

	rows, err := s.db.Raw(query, args...).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var item ExportStreamItem
		if err := s.db.ScanRows(rows, &item); err != nil {
			return err
		}
		if err := decodeCommandItem(&item); err != nil {
			return err
		}
		if err := fn(item); err != nil {
			return err
		}
	}
	return rows.Err()
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
	err := s.db.Where("overlay_type IN ('command_hint', 'button', 'substitution', 'gag', 'bell') AND log_entry_id IN ?", ids).
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

func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*':
			b.WriteString("[*]")
		case '?':
			b.WriteString("[?]")
		case '[':
			b.WriteString("[[]")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) SearchLogsDetailed(sessionID int64, query string, contextLines int, beforeID int64) ([][]LogEntry, int64, error) {
	// Build a GLOB pattern for case-insensitive Unicode search.
	// GLOB in SQLite is case-sensitive, but we can make it insensitive by using [aA] patterns.
	escaped := escapeGlob(query)
	var globPattern strings.Builder
	globPattern.WriteByte('*')
	for _, r := range escaped {
		lower := unicode.ToLower(r)
		upper := unicode.ToUpper(r)
		if lower == upper {
			globPattern.WriteRune(lower)
		} else {
			globPattern.WriteByte('[')
			globPattern.WriteRune(lower)
			globPattern.WriteRune(upper)
			globPattern.WriteByte(']')
		}
	}
	globPattern.WriteByte('*')
	pattern := globPattern.String()

	// Match log entries by MUD output text.
	var textMatchIDs []int64
	db := s.db.Model(&LogRecord{}).Where("session_id = ? AND plain_text GLOB ?", sessionID, pattern).Where(visibleLogEntrySQL)

	if beforeID > 0 {
		db = db.Where("id < ?", beforeID)
	}
	// Limit results per page to 50 matches to avoid overwhelming the UI
	if err := db.Order("id DESC").Limit(50).Pluck("id", &textMatchIDs).Error; err != nil {
		return nil, 0, err
	}

	// Also match log entries where a sent command (command_hint overlay) contains the query.
	var cmdMatchIDs []int64
	cmdDB := s.db.Model(&LogOverlay{}).
		Where("overlay_type = 'command_hint' AND payload_json GLOB ?", pattern).
		Where("log_entry_id IN (SELECT id FROM log_entries WHERE session_id = ? AND "+visibleLogEntrySQL+")", sessionID)

	if beforeID > 0 {
		cmdDB = cmdDB.Where("log_entry_id < ?", beforeID)
	}
	if err := cmdDB.Order("log_entry_id DESC").Limit(50).Pluck("log_entry_id", &cmdMatchIDs).Error; err != nil {
		return nil, 0, err
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
		return nil, 0, nil
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
	// Sort IDs ascending to prepare for grouping chronologically,
	// but we will reverse the final groups to show newest first.
	sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })

	var records []LogRecord
	if err := s.db.Where("id IN ?", allIDs).Where(visibleLogEntrySQL).Order("id ASC").Find(&records).Error; err != nil {
		return nil, 0, err
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
		return nil, 0, err
	}

	entryMap := make(map[int64]LogEntry)
	for _, e := range allEntries {
		entryMap[e.ID] = e
	}

	var groups [][]LogEntry
	if len(records) == 0 {
		return nil, 0, nil
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

	// Reverse groups to show newest first
	for i, j := 0, len(groups)-1; i < j; i, j = i+1, j-1 {
		groups[i], groups[j] = groups[j], groups[i]
	}

	var cursor int64
	if len(matchingIDs) > 0 {
		cursor = matchingIDs[0]
		for _, mid := range matchingIDs {
			if mid < cursor {
				cursor = mid
			}
		}
	}

	return groups, cursor, nil
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
