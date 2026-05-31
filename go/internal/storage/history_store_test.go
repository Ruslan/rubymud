package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHistoryStore(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)

	// Test Append and List
	if err := store.AppendHistoryEntry(sessionID, "input", "test command 1"); err != nil {
		t.Fatalf("AppendHistoryEntry: %v", err)
	}
	if err := store.AppendHistoryEntry(sessionID, "input", "test command 2"); err != nil {
		t.Fatalf("AppendHistoryEntry: %v", err)
	}

	entries, err := store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	if entries[0].Line != "test command 2" {
		t.Errorf("got %s, want test command 2", entries[0].Line)
	}

	// Test Delete
	entryID := entries[0].ID
	if err := store.DeleteHistoryEntry(sessionID, entryID); err != nil {
		t.Fatalf("DeleteHistoryEntry: %v", err)
	}

	entries, err = store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory after delete: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d entries after delete, want 1", len(entries))
	}

	if entries[0].Line != "test command 1" {
		t.Errorf("got %s, want test command 1", entries[0].Line)
	}
}

func TestAppendHistoryEntryUpsertsByLineAndRefreshesTimestamp(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)

	if err := store.AppendHistoryEntry(sessionID, "input", "look"); err != nil {
		t.Fatalf("AppendHistoryEntry first: %v", err)
	}
	first, err := store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory first: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first len = %d, want 1", len(first))
	}
	firstCreatedAt := first[0].CreatedAt.Time

	if err := store.AppendHistoryEntry(sessionID, "input", "north"); err != nil {
		t.Fatalf("AppendHistoryEntry north: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	if err := store.AppendHistoryEntry(sessionID, "expanded", "look"); err != nil {
		t.Fatalf("AppendHistoryEntry upsert: %v", err)
	}

	entries, err := store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].Line != "look" {
		t.Fatalf("newest line = %q, want look", entries[0].Line)
	}
	if entries[1].Line != "north" {
		t.Fatalf("second line = %q, want north", entries[1].Line)
	}
	if entries[0].Kind != "input" {
		t.Fatalf("kind = %q, want input (preferred for recall)", entries[0].Kind)
	}
	if !entries[0].CreatedAt.Time.After(firstCreatedAt) {
		t.Fatalf("created_at = %v, want after %v", entries[0].CreatedAt.Time, firstCreatedAt)
	}
}

func TestHistoryUniqueLineMigrationDedupesExistingRows(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, version := range []string{
		"001_init.sql", "002_timers.sql", "003_hotkey_mobile_layout.sql", "004_profile_timers.sql",
		"005_timer_repeat_mode.sql", "006_mccp_settings.sql", "007_substitute_rules.sql",
		"008_profile_timer_subscription_removals.sql", "009_session_ansi_theme.sql",
	} {
		if err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version).Error; err != nil {
			t.Fatalf("insert schema migration %s: %v", version, err)
		}
	}
	if err := db.Exec(`
		CREATE TABLE history_entries (
		  id INTEGER PRIMARY KEY,
		  session_id INTEGER NOT NULL,
		  kind TEXT NOT NULL,
		  line TEXT NOT NULL,
		  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`).Error; err != nil {
		t.Fatalf("create history_entries: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO history_entries (id, session_id, kind, line, created_at) VALUES
		  (1, 1, 'expanded', 'look', '2026-06-01 10:00:00'),
		  (2, 1, 'input', 'look', '2026-06-01 10:01:00'),
		  (3, 1, 'trigger', 'north', '2026-06-01 10:02:00')
	`).Error; err != nil {
		t.Fatalf("insert duplicate history rows: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	entries, err := store.ListHistory(1, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.Line == "look" && entry.Kind != "input" {
			t.Fatalf("look kind = %q, want input after dedupe migration", entry.Kind)
		}
	}
	if err := store.AppendHistoryEntry(1, "expanded", "look"); err != nil {
		t.Fatalf("AppendHistoryEntry after migration: %v", err)
	}
	entries, err = store.ListHistory(1, 10)
	if err != nil {
		t.Fatalf("ListHistory after append: %v", err)
	}
	lookCount := 0
	for _, entry := range entries {
		if entry.Line == "look" {
			lookCount++
		}
	}
	if lookCount != 1 {
		t.Fatalf("look entries = %d, want 1", lookCount)
	}
}

func TestAppendHistoryEntryConcurrentUpsertsKeepSingleRow(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(8)

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)
	const workers = 64

	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		kind := "expanded"
		if i%2 == 0 {
			kind = "input"
		}
		go func(kind string) {
			defer wg.Done()
			errCh <- store.AppendHistoryEntry(sessionID, kind, "look")
		}(kind)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("AppendHistoryEntry concurrent: %v", err)
		}
	}

	entries, err := store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if entries[0].Line != "look" {
		t.Fatalf("line = %q, want look", entries[0].Line)
	}
	if entries[0].Kind != "input" {
		t.Fatalf("kind = %q, want input", entries[0].Kind)
	}
}

func TestListHistoryPageSearchFiltersAndPaginates(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)

	for _, entry := range []struct {
		kind string
		line string
	}{
		{kind: "input", line: "look statue"},
		{kind: "expanded", line: "LOOK"},
		{kind: "input", line: "kill goblin"},
		{kind: "input", line: "look north"},
	} {
		if err := store.AppendHistoryEntry(sessionID, entry.kind, entry.line); err != nil {
			t.Fatalf("AppendHistoryEntry(%s): %v", entry.line, err)
		}
	}

	firstPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Query: "look", Limit: 2})
	if err != nil {
		t.Fatalf("ListHistoryPage search first page: %v", err)
	}
	if !hasMore {
		t.Fatal("search first page hasMore = false, want true")
	}
	if got := historyLines(firstPage); fmt.Sprint(got) != "[look north LOOK]" {
		t.Fatalf("search first page lines = %v, want [look north LOOK]", got)
	}

	secondPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Query: "look", Limit: 2, BeforeID: firstPage[len(firstPage)-1].ID})
	if err != nil {
		t.Fatalf("ListHistoryPage search second page: %v", err)
	}
	if hasMore {
		t.Fatal("search second page hasMore = true, want false")
	}
	if got := historyLines(secondPage); fmt.Sprint(got) != "[look statue]" {
		t.Fatalf("search second page lines = %v, want [look statue]", got)
	}

	inputPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Kind: "input", Query: "look", Limit: 5})
	if err != nil {
		t.Fatalf("ListHistoryPage kind+search filter: %v", err)
	}
	if hasMore {
		t.Fatal("input search page hasMore = true, want false")
	}
	if got := historyLines(inputPage); fmt.Sprint(got) != "[look north look statue]" {
		t.Fatalf("input search page lines = %v, want [look north look statue]", got)
	}
}

func TestListHistoryPageSearchEscapesGlobSpecialCharacters(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)

	for _, entry := range []struct {
		kind string
		line string
	}{
		{kind: "input", line: "cast *"},
		{kind: "input", line: "ask ?"},
		{kind: "input", line: "show [hp]"},
		{kind: "input", line: "plain text"},
	} {
		if err := store.AppendHistoryEntry(sessionID, entry.kind, entry.line); err != nil {
			t.Fatalf("AppendHistoryEntry(%s): %v", entry.line, err)
		}
	}

	star, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Query: "*", Limit: 10})
	if err != nil {
		t.Fatalf("ListHistoryPage star query: %v", err)
	}
	if hasMore {
		t.Fatal("star query hasMore = true, want false")
	}
	if got := historyLines(star); fmt.Sprint(got) != "[cast *]" {
		t.Fatalf("star query lines = %v, want [cast *]", got)
	}

	question, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Query: "?", Limit: 10})
	if err != nil {
		t.Fatalf("ListHistoryPage question query: %v", err)
	}
	if hasMore {
		t.Fatal("question query hasMore = true, want false")
	}
	if got := historyLines(question); fmt.Sprint(got) != "[ask ?]" {
		t.Fatalf("question query lines = %v, want [ask ?]", got)
	}

	bracket, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Query: "[hp]", Limit: 10})
	if err != nil {
		t.Fatalf("ListHistoryPage bracket query: %v", err)
	}
	if hasMore {
		t.Fatal("bracket query hasMore = true, want false")
	}
	if got := historyLines(bracket); fmt.Sprint(got) != "[show [hp]]" {
		t.Fatalf("bracket query lines = %v, want [show [hp]]", got)
	}
}

func TestListHistoryPageFiltersAndPaginates(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)

	for _, entry := range []struct {
		kind string
		line string
	}{
		{kind: "input", line: "raw look"},
		{kind: "expanded", line: "look"},
		{kind: "input", line: "raw kill"},
		{kind: "trigger", line: "stand"},
	} {
		if err := store.AppendHistoryEntry(sessionID, entry.kind, entry.line); err != nil {
			t.Fatalf("AppendHistoryEntry(%s): %v", entry.line, err)
		}
	}

	firstPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListHistoryPage first page: %v", err)
	}
	if !hasMore {
		t.Fatal("first page hasMore = false, want true")
	}
	if got := historyLines(firstPage); fmt.Sprint(got) != "[stand raw kill]" {
		t.Fatalf("first page lines = %v, want [stand raw kill]", got)
	}

	secondPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Limit: 2, BeforeID: firstPage[len(firstPage)-1].ID})
	if err != nil {
		t.Fatalf("ListHistoryPage second page: %v", err)
	}
	if hasMore {
		t.Fatal("second page hasMore = true, want false")
	}
	if got := historyLines(secondPage); fmt.Sprint(got) != "[look raw look]" {
		t.Fatalf("second page lines = %v, want [look raw look]", got)
	}

	inputPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Kind: "input", Limit: 1})
	if err != nil {
		t.Fatalf("ListHistoryPage input filter: %v", err)
	}
	if !hasMore {
		t.Fatal("input page hasMore = false, want true")
	}
	if len(inputPage) != 1 || inputPage[0].Kind != "input" || inputPage[0].Line != "raw kill" {
		t.Fatalf("input page = %+v, want latest raw input", inputPage)
	}
}

func TestListHistoryPageCursorMatchesCreatedAtIDOrder(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	for _, entry := range []HistoryEntry{
		{SessionID: sessionID, Kind: "input", Line: "newest low id", CreatedAt: SQLiteTime{Time: base.Add(2 * time.Minute)}},
		{SessionID: sessionID, Kind: "input", Line: "same time low id", CreatedAt: SQLiteTime{Time: base.Add(time.Minute)}},
		{SessionID: sessionID, Kind: "input", Line: "same time high id", CreatedAt: SQLiteTime{Time: base.Add(time.Minute)}},
		{SessionID: sessionID, Kind: "input", Line: "oldest high id", CreatedAt: SQLiteTime{Time: base}},
	} {
		if err := store.db.Create(&entry).Error; err != nil {
			t.Fatalf("Create(%s): %v", entry.Line, err)
		}
	}

	firstPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListHistoryPage first page: %v", err)
	}
	if !hasMore {
		t.Fatal("first page hasMore = false, want true")
	}
	if got := historyLines(firstPage); fmt.Sprint(got) != "[newest low id same time high id]" {
		t.Fatalf("first page lines = %v, want [newest low id same time high id]", got)
	}

	secondPage, hasMore, err := store.ListHistoryPage(sessionID, HistoryListOptions{Limit: 2, BeforeID: firstPage[len(firstPage)-1].ID})
	if err != nil {
		t.Fatalf("ListHistoryPage second page: %v", err)
	}
	if hasMore {
		t.Fatal("second page hasMore = true, want false")
	}
	if got := historyLines(secondPage); fmt.Sprint(got) != "[same time low id oldest high id]" {
		t.Fatalf("second page lines = %v, want [same time low id oldest high id]", got)
	}
}

func historyLines(entries []HistoryEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.Line)
	}
	return lines
}
