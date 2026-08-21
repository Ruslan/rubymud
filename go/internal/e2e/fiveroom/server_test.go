package fiveroom

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerSendsRMUDBlocksIgnoresTelnetAndTracesCommands(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	server, err := Listen("127.0.0.1:0", tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	conn, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if got := readBlock(t, reader); !strings.Contains(got, "Loop Room 1") || !strings.Contains(got, "[ Exits: E ]") {
		t.Fatalf("initial block = %q", got)
	}
	if _, err := conn.Write([]byte("scan\r\n")); err != nil {
		t.Fatal(err)
	}
	scan, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(scan, "На востоке: Loop Room 2.") {
		t.Fatalf("scan = %q err=%v", scan, err)
	}
	if blank, err := reader.ReadString('\n'); err != nil || strings.TrimSpace(blank) != "" {
		t.Fatalf("scan boundary = %q err=%v", blank, err)
	}

	if _, err := conn.Write(append([]byte{255, 253, 86}, []byte("E\r\n")...)); err != nil {
		t.Fatal(err)
	}
	if got := readBlock(t, reader); !strings.Contains(got, "Loop Room 2") {
		t.Fatalf("move block = %q", got)
	}
	if _, err := conn.Write([]byte("N\r\n")); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "не можете идти") {
		t.Fatalf("refusal = %q err=%v", line, err)
	}

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("trace lines=%d: %s", len(lines), data)
	}
	var first TraceEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.From != 1 || first.To != 1 || first.Command != "scan" || first.Moved {
		t.Fatalf("first trace = %+v", first)
	}
}

func readBlock(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(line)
	}
	return b.String()
}
