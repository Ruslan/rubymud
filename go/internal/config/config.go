package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var keyPattern = regexp.MustCompile(`^\s*key\s+['"]([^'"]+)['"]\s*,\s*['"]([^'"]*)['"]`)

type Hotkey struct {
	Shortcut string `json:"shortcut"`
	Command  string `json:"command"`
}

func LoadHotkeys(path string) ([]Hotkey, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hotkeys := []Hotkey{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := keyPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}

		hotkeys = append(hotkeys, Hotkey{
			Shortcut: strings.TrimSpace(match[1]),
			Command:  strings.TrimSpace(match[2]),
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return hotkeys, nil
}
