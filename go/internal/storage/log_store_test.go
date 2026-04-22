package storage

import (
	"fmt"
	"testing"

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

func TestSearchLogsDetailed_OverlappingContext(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	// Create a sequence of logs
	for i := 1; i <= 20; i++ {
		text := fmt.Sprintf("Line %d", i)
		if i == 5 || i == 7 {
			text += " [MATCH]"
		}
		s.AppendLogEntry(sessionID, text, text)
	}

	// Search with context 2. 
	// Match at 5 should get [3, 4, 5, 6, 7]
	// Match at 7 should get [5, 6, 7, 8, 9]
	// Since they overlap, they should be merged into one group [3, 4, 5, 6, 7, 8, 9]
	
	groups, err := s.SearchLogsDetailed(sessionID, "[MATCH]", 2)
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
		s.AppendLogEntry(sessionID, text, text)
	}

	groups, err := s.SearchLogsDetailed(sessionID, "[MATCH]", 2)
	if err != nil {
		t.Fatalf("SearchLogsDetailed: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("Expected 2 separate groups, got %d", len(groups))
	}

	if len(groups[0]) != 5 || groups[0][2].PlainText != "Line 10 [MATCH]" {
		t.Errorf("Group 1 mismatch: size=%d, center=%q", len(groups[0]), groups[0][2].PlainText)
	}
	if len(groups[1]) != 5 || groups[1][2].PlainText != "Line 40 [MATCH]" {
		t.Errorf("Group 2 mismatch: size=%d, center=%q", len(groups[1]), groups[1][2].PlainText)
	}
}

func TestLogRangeDetailed(t *testing.T) {
	s := newLogStoreTestStore(t)
	sessionID := int64(1)

	for i := 1; i <= 10; i++ {
		text := fmt.Sprintf("Line %d", i)
		s.AppendLogEntry(sessionID, text, text)
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
