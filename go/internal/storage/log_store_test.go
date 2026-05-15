package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newLogStoreTestStore(t *testing.T) *Store {
	t.Helper()
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	db.AutoMigrate(&LogRecord{}, &LogOverlay{})

	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	return &Store{db: db}
}

func TestAppendLogEntryWithOverlaysFiltersGagAndIsAtomic(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	_, err := s.AppendLogEntryWithOverlays(sessionID, "main", "spam", "spam", []LogOverlay{{
		OverlayType: "gag",
		PayloadJSON: `{"rule_id":1}`,
		SourceType:  "substitute_rule",
		SourceID:    "1",
	}})
	if err != nil {
		t.Fatalf("AppendLogEntryWithOverlays gag: %v", err)
	}

	var overlayCount int64
	if err := s.db.Model(&LogOverlay{}).Where("overlay_type = ?", "gag").Count(&overlayCount).Error; err != nil {
		t.Fatalf("count overlays: %v", err)
	}
	if overlayCount != 1 {
		t.Fatalf("gag overlays = %d, want 1", overlayCount)
	}

	visible, err := s.RecentLogs(sessionID, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("visible logs = %+v, want none", visible)
	}
	groups, _, err := s.SearchLogsDetailed(sessionID, "spam", 1, 9223372036854775807)
	if err != nil {
		t.Fatalf("SearchLogsDetailed: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("search groups = %+v, want none", groups)
	}
}

func TestSearchLogsDetailed_OverlappingContext(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	// Create a sequence of logs
	for i := 1; i <= 20; i++ {
		text := fmt.Sprintf("Line %d", i)
		if i == 5 || i == 7 {
			text += " [MATCH]"
		}
		s.AppendLogEntry(sessionID, "main", text, text)
	}

	// Search with context 2.
	// Match at 5 should get [3, 4, 5, 6, 7]
	// Match at 7 should get [5, 6, 7, 8, 9]
	// Since they overlap, they should be merged into one group [3, 4, 5, 6, 7, 8, 9]

	groups, _, err := s.SearchLogsDetailed(sessionID, "[MATCH]", 2, 9223372036854775807)
	if err != nil {
		t.Fatalf("SearchLogsDetailed: %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("Expected 1 merged group, got %d", len(groups))
	}

	group := groups[0]
	if len(group) != 7 {
		t.Errorf("Expected group size 7, got %d", len(group))
	}
	if group[0].PlainText != "Line 3" || group[len(group)-1].PlainText != "Line 9" {
		t.Errorf("Group range mismatch: first=%q, last=%q", group[0].PlainText, group[len(group)-1].PlainText)
	}
}

func TestSearchLogsDetailed_SeparateGroups(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	// Matches far apart
	for i := 1; i <= 50; i++ {
		text := fmt.Sprintf("Line %d", i)
		if i == 10 || i == 40 {
			text += " [MATCH]"
		}
		s.AppendLogEntry(sessionID, "main", text, text)
	}

	groups, _, err := s.SearchLogsDetailed(sessionID, "[MATCH]", 2, 9223372036854775807)
	if err != nil {
		t.Fatalf("SearchLogsDetailed: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("Expected 2 separate groups, got %d", len(groups))
	}

	if len(groups[0]) != 5 || groups[0][2].PlainText != "Line 40 [MATCH]" {
		t.Errorf("Group 1 mismatch: size=%d, center=%q", len(groups[0]), groups[0][2].PlainText)
	}
	if len(groups[1]) != 5 || groups[1][2].PlainText != "Line 10 [MATCH]" {
		t.Errorf("Group 2 mismatch: size=%d, center=%q", len(groups[1]), groups[1][2].PlainText)
	}
}

func TestLogRangeDetailed(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	for i := 1; i <= 10; i++ {
		text := fmt.Sprintf("Line %d", i)
		s.AppendLogEntry(sessionID, "main", text, text)
	}

	// Get logs before ID 6 (exclusive), limit 3. Should get 3, 4, 5
	entries, err := s.LogRangeDetailed(sessionID, 6, 3)
	if err != nil {
		t.Fatalf("LogRangeDetailed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}
	if entries[0].PlainText != "Line 3" || entries[2].PlainText != "Line 5" {
		t.Errorf("Entries mismatch: first=%q, last=%q", entries[0].PlainText, entries[2].PlainText)
	}
}

func TestLogRangeByDate(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	now := nowSQLiteTime()
	yesterday := SQLiteTime{Time: now.Add(-24 * time.Hour)}
	tomorrow := SQLiteTime{Time: now.Add(24 * time.Hour)}

	// Create some logs
	s.AppendLogEntry(sessionID, "main", "Old Log", "Old Log")
	// Manually update CreatedAt for testing range
	s.db.Model(&LogRecord{}).Where("plain_text = ?", "Old Log").Update("created_at", yesterday)

	s.AppendLogEntry(sessionID, "main", "New Log 1", "New Log 1")
	s.AppendLogEntry(sessionID, "main", "New Log 2", "New Log 2")

	// Test: Get only "Old Log"
	entries, total, err := s.LogRangeByDate(sessionID, SQLiteTime{Time: yesterday.Add(-1 * time.Hour)}, SQLiteTime{Time: yesterday.Add(1 * time.Hour)}, 10, 0)
	if err != nil {
		t.Fatalf("LogRangeByDate error: %v", err)
	}
	if total != 1 || len(entries) != 1 || entries[0].PlainText != "Old Log" {
		t.Errorf("Expected 1 'Old Log', got total=%d, count=%d, first=%q", total, len(entries), entries[0].PlainText)
	}

	// Test: Get all 3 logs
	entries, total, err = s.LogRangeByDate(sessionID, SQLiteTime{Time: yesterday.Add(-1 * time.Hour)}, tomorrow, 10, 0)
	if err != nil {
		t.Fatalf("LogRangeByDate error: %v", err)
	}
	if total != 3 || len(entries) != 3 {
		t.Errorf("Expected 3 logs, got total=%d, count=%d", total, len(entries))
	}

	// Test: Pagination (Page 2)
	entries, total, err = s.LogRangeByDate(sessionID, SQLiteTime{Time: yesterday.Add(-1 * time.Hour)}, tomorrow, 2, 2)
	if err != nil {
		t.Fatalf("LogRangeByDate error: %v", err)
	}
	if total != 3 || len(entries) != 1 || entries[0].PlainText != "New Log 2" {
		t.Errorf("Expected 1 log (New Log 2) on page 2, got total=%d, count=%d, first=%q", total, len(entries), entries[0].PlainText)
	}
}

func TestLogEntryByID_ExcludesGagged(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	id, err := s.AppendLogEntryWithOverlays(sessionID, "main", "visible", "visible", nil)
	if err != nil {
		t.Fatalf("AppendLogEntryWithOverlays visible: %v", err)
	}

	gagID, err := s.AppendLogEntryWithOverlays(sessionID, "main", "hidden", "hidden", []LogOverlay{{
		OverlayType: "gag",
		PayloadJSON: `{"rule_id":1}`,
	}})
	if err != nil {
		t.Fatalf("AppendLogEntryWithOverlays gag: %v", err)
	}

	entry, err := s.LogEntryByID(sessionID, id)
	if err != nil {
		t.Fatalf("LogEntryByID visible: %v", err)
	}
	if entry.ID != id {
		t.Errorf("expected visible entry id=%d, got %d", id, entry.ID)
	}

	_, err = s.LogEntryByID(sessionID, gagID)
	if err == nil {
		t.Fatal("expected error for gagged entry, got nil")
	}
}
