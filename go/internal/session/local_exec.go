package session

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	localExecTimeout        = 5 * time.Second
	localExecMaxOutputBytes = 64 * 1024
	localExecMaxOutputLines = 200
)

type cappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Buffer.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = b.Buffer.Write(p)
		} else {
			_, _ = b.Buffer.Write(p[:remaining])
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func validateLocalExecPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty path")
	}
	if strings.ContainsRune(path, '\x00') || strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf("path contains control characters")
	}
	if filepath.Base(path) == path {
		return fmt.Errorf("path must be explicit, for example ./items_db_client")
	}
	return nil
}

func splitExecOutputLines(output string) []string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return nil
	}
	rawLines := strings.Split(output, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		sanitized, _ := sanitizeLineControlsAndBEL(line)
		lines = append(lines, sanitized)
	}
	return lines
}

func (s *Session) runLocalExec(path string, args []string) []string {
	if err := validateLocalExecPath(path); err != nil {
		return []string{fmt.Sprintf("#exec: %v", err)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), localExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	var output cappedBuffer
	output.limit = localExecMaxOutputBytes
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	lines := splitExecOutputLines(output.String())
	if output.truncated {
		lines = append(lines, fmt.Sprintf("#exec: output truncated after %d bytes", localExecMaxOutputBytes))
	}
	if len(lines) > localExecMaxOutputLines {
		lines = append(lines[:localExecMaxOutputLines], fmt.Sprintf("#exec: output truncated after %d lines", localExecMaxOutputLines))
	}
	if ctx.Err() == context.DeadlineExceeded {
		return append(lines, fmt.Sprintf("#exec: timed out after %s", localExecTimeout))
	}
	if err != nil {
		lines = append(lines, fmt.Sprintf("#exec: %v", err))
	}
	return lines
}
