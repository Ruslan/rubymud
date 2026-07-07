package storage

import (
	"testing"
	"time"
)

// TestStreamExportLogEqualTimestampOrder verifies the streaming path's tiebreak:
// at an EQUAL created_at, a command sorts before output (ORDER BY sort_key ASC,
// kind ASC, row_id ASC; 'command' < 'output'). Also confirms StreamExportLog
// decodes command payloads.
func TestStreamExportLogEqualTimestampOrder(t *testing.T) {
	s := newLogStoreTestStore(t)
	at := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	// Output line and a command_hint overlay at the SAME created_at.
	outID, err := s.AppendLogEntryWithOverlays(1, "main", "OUTPUT", "OUTPUT", nil)
	if err != nil {
		t.Fatalf("AppendLogEntryWithOverlays: %v", err)
	}
	if err := s.db.Model(&LogRecord{}).Where("id = ?", outID).
		Update("created_at", SQLiteTime{Time: at}).Error; err != nil {
		t.Fatalf("set output created_at: %v", err)
	}
	if err := s.db.Create(&LogOverlay{
		LogEntryID:  outID,
		OverlayType: "command_hint",
		PayloadJSON: `{"command":"kill dragon"}`,
		SourceType:  "client",
		CreatedAt:   SQLiteTime{Time: at},
	}).Error; err != nil {
		t.Fatalf("create command_hint: %v", err)
	}

	var order []string
	if err := s.StreamExportLog(ExportStreamOptions{SessionID: 1, IncludeCommands: true},
		func(item ExportStreamItem) error {
			order = append(order, item.Kind+":"+item.Ansi)
			return nil
		}); err != nil {
		t.Fatalf("StreamExportLog: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 rows, got %v", order)
	}
	if order[0] != "command:kill dragon" {
		t.Fatalf("command should sort before output at equal timestamp; got %v", order)
	}
	if order[1] != "output:OUTPUT" {
		t.Fatalf("output should be second; got %v", order)
	}
}

// TestStreamExportLogExcludesLocalEcho verifies that local client echo
// (source_type="echo", written via AppendLogEntry) is excluded from the export
// stream, while genuine server output (source_type="mud", written via
// AppendLogEntryWithOverlays) is included.
func TestStreamExportLogExcludesLocalEcho(t *testing.T) {
	s := newLogStoreTestStore(t)

	// Real server output.
	serverID, err := s.AppendLogEntryWithOverlays(1, "main", "A dragon appears.", "A dragon appears.", nil)
	if err != nil {
		t.Fatalf("AppendLogEntryWithOverlays: %v", err)
	}
	// Local client echo (#showme etc.).
	echoID, err := s.AppendLogEntry(1, "main", "you see nothing special", "you see nothing special")
	if err != nil {
		t.Fatalf("AppendLogEntry: %v", err)
	}

	// source_type values are distinct.
	var serverRec, echoRec LogRecord
	if err := s.db.First(&serverRec, serverID).Error; err != nil {
		t.Fatalf("load server rec: %v", err)
	}
	if err := s.db.First(&echoRec, echoID).Error; err != nil {
		t.Fatalf("load echo rec: %v", err)
	}
	if serverRec.SourceType != "mud" {
		t.Fatalf("server output source_type = %q, want %q", serverRec.SourceType, "mud")
	}
	if echoRec.SourceType != "echo" {
		t.Fatalf("local echo source_type = %q, want %q", echoRec.SourceType, "echo")
	}

	items := collectStream(t, s, ExportStreamOptions{SessionID: 1, IncludeCommands: false})

	var gotServer, gotEcho bool
	for _, it := range items {
		switch it.Ansi {
		case "A dragon appears.":
			gotServer = true
		case "you see nothing special":
			gotEcho = true
		}
	}
	if !gotServer {
		t.Fatalf("server output missing from export: %+v", items)
	}
	if gotEcho {
		t.Fatalf("local echo leaked into export: %+v", items)
	}
}

// collectStream drains StreamExportLog into a slice (test convenience).
func collectStream(t *testing.T, s *Store, opts ExportStreamOptions) []ExportStreamItem {
	t.Helper()
	var items []ExportStreamItem
	if err := s.StreamExportLog(opts, func(it ExportStreamItem) error {
		items = append(items, it)
		return nil
	}); err != nil {
		t.Fatalf("StreamExportLog: %v", err)
	}
	return items
}

// TestStreamExportLogRawContentIncludesGagsExcludesSubstitutions verifies the
// RAW/minimalistic guarantee (migrated from the removed export-data tests): the
// export streams raw_text (pre-overlay) so substitution overlays are NOT baked,
// and gagged lines ARE included (nothing hidden).
func TestStreamExportLogRawContentIncludesGagsExcludesSubstitutions(t *testing.T) {
	s := newLogStoreTestStore(t)
	at := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	// A line with a substitution overlay -> raw text must be exported, not the
	// replacement.
	subID, err := s.AppendLogEntryWithOverlays(1, "main", "\x1b[31moriginal\x1b[0m", "original", nil)
	if err != nil {
		t.Fatalf("append sub line: %v", err)
	}
	if err := s.db.Create(&LogOverlay{
		LogEntryID: subID, OverlayType: "substitution",
		PayloadJSON: `{"replacement":"REPLACED"}`, SourceType: "sub", CreatedAt: SQLiteTime{Time: at},
	}).Error; err != nil {
		t.Fatalf("create substitution: %v", err)
	}

	// A gagged line -> must still be present.
	gagID, err := s.AppendLogEntryWithOverlays(1, "main", "\x1b[32msecret\x1b[0m", "secret", nil)
	if err != nil {
		t.Fatalf("append gag line: %v", err)
	}
	if err := s.db.Create(&LogOverlay{
		LogEntryID: gagID, OverlayType: "gag",
		PayloadJSON: `{}`, SourceType: "sub", CreatedAt: SQLiteTime{Time: at.Add(time.Second)},
	}).Error; err != nil {
		t.Fatalf("create gag: %v", err)
	}

	items := collectStream(t, s, ExportStreamOptions{SessionID: 1, IncludeCommands: true})

	var gotRaw, gotGag bool
	for _, it := range items {
		if it.Ansi == "REPLACED" {
			t.Fatalf("substitution was baked into export ansi: %+v", it)
		}
		if it.Ansi == "\x1b[31moriginal\x1b[0m" {
			gotRaw = true
		}
		if it.Ansi == "\x1b[32msecret\x1b[0m" {
			gotGag = true
		}
	}
	if !gotRaw {
		t.Fatalf("raw (pre-substitution) text missing from export: %+v", items)
	}
	if !gotGag {
		t.Fatalf("gagged line must be included in export: %+v", items)
	}
}
