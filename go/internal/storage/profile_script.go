package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

type ProfileScript struct {
	Name              string
	Description       string
	Aliases           []AliasRule
	Triggers          []TriggerRule
	Highlights        []HighlightRule
	Hotkeys           []HotkeyRule
	DeclaredVariables []ProfileVariable
}


func splitBraceArg(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}

	delim := s[0]
	closer := byte('}')
	switch delim {
	case '{':
		closer = '}'
	case '\'':
		closer = '\''
	case '"':
		closer = '"'
	default:
		parts := strings.SplitN(s, " ", 2)
		if len(parts) == 1 {
			return parts[0], ""
		}
		return parts[0], parts[1]
	}

	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == byte(delim) && i == 0 {
			depth = 1
			continue
		}
		if s[i] == delim && delim != closer {
			depth++
		}
		if s[i] == closer {
			depth--
			if depth == 0 {
				return s[1:i], strings.TrimSpace(s[i+1:])
			}
		}
	}

	return strings.Trim(s, string(delim)), ""
}

func (s *Store) ExportProfileScript(profileID int64) (string, error) {
	p, err := s.GetProfile(profileID)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("#nop Profile: %s\n", p.Name))
	if p.Description != "" {
		sb.WriteString(fmt.Sprintf("#nop Description: %s\n", p.Description))
	}

	meta := map[string]string{"version": "1", "exported": time.Now().UTC().Format(time.RFC3339)}
	metaJson, _ := json.Marshal(meta)
	sb.WriteString(fmt.Sprintf("#nop rubymud:profile %s\n\n", string(metaJson)))

	dvars, _ := s.ListProfileVariables(profileID)
	if len(dvars) > 0 {
		for _, dv := range dvars {
			if dv.Description != "" {
				meta := map[string]string{"description": dv.Description}
				mj, _ := json.Marshal(meta)
				sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))
			}
			sb.WriteString(fmt.Sprintf("#var {%s} {%s}\n", dv.Name, dv.DefaultValue))
		}
		sb.WriteString("\n")
	}

	aliases, _ := s.ListAliases(profileID)
	if len(aliases) > 0 {
		for _, a := range aliases {
			sb.WriteString(fmt.Sprintf("#alias {%s} {%s}\n", a.Name, a.Template))
		}
		sb.WriteString("\n")
	}

	triggers, _ := s.ListTriggers(profileID)
	if len(triggers) > 0 {
		for _, t := range triggers {
			meta := make(map[string]any)
			if t.StopAfterMatch {
				meta["stop_after_match"] = true
			}
			if t.IsButton {
				meta["is_button"] = true
			}
			if t.Name != "" && t.Name != t.Pattern {
				meta["name"] = t.Name
			}
			if len(meta) > 0 {
				mj, _ := json.Marshal(meta)
				sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))
			}
			sb.WriteString(fmt.Sprintf("#action {%s} {%s}", t.Pattern, t.Command))
			if t.GroupName != "default" && t.GroupName != "" {
				sb.WriteString(fmt.Sprintf(" {%s}", t.GroupName))
			} else if t.IsButton {
				sb.WriteString(" {}") // Empty group to allow button parsing
			}
			if t.IsButton {
				sb.WriteString(" {button}")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	highlights, _ := s.ListHighlights(profileID)
	if len(highlights) > 0 {
		for _, h := range highlights {
			meta := map[string]any{
				"bold": h.Bold, "faint": h.Faint, "italic": h.Italic,
				"underline": h.Underline, "strikethrough": h.Strikethrough,
				"blink": h.Blink, "reverse": h.Reverse, "bg": h.BG,
			}
			mj, _ := json.Marshal(meta)
			sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))

			fg := h.FG
			if fg == "" {
				fg = "default"
			}
			sb.WriteString(fmt.Sprintf("#highlight {%s} {%s}", fg, h.Pattern))
			if h.GroupName != "default" && h.GroupName != "" {
				sb.WriteString(fmt.Sprintf(" {%s}", h.GroupName))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	hotkeys, _ := s.ListHotkeys(profileID)
	if len(hotkeys) > 0 {
		for _, hk := range hotkeys {
			sb.WriteString(fmt.Sprintf("#hotkey {%s} {%s}\n", hk.Shortcut, hk.Command))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func ParseProfileScript(text string) (*ProfileScript, error) {
	ps := &ProfileScript{}
	scanner := bufio.NewScanner(strings.NewReader(text))

	var pendingMeta map[string]any

	aliasPos := 1
	triggerPos := 1
	hlPos := 1
	hkPos := 1
	varPos := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#nop ") {
			nopText := strings.TrimSpace(strings.TrimPrefix(line, "#nop "))
			if strings.HasPrefix(nopText, "Profile: ") {
				if ps.Name == "" {
					ps.Name = strings.TrimSpace(strings.TrimPrefix(nopText, "Profile: "))
				}
			} else if strings.HasPrefix(nopText, "Description: ") {
				if ps.Description == "" {
					ps.Description = strings.TrimSpace(strings.TrimPrefix(nopText, "Description: "))
				}
			} else if strings.HasPrefix(nopText, "rubymud:rule ") {
				jsonText := strings.TrimSpace(strings.TrimPrefix(nopText, "rubymud:rule "))
				json.Unmarshal([]byte(jsonText), &pendingMeta)
			}
			continue
		}

		if strings.HasPrefix(line, "#alias ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#alias "))
			name, rest := splitBraceArg(rest)
			template, _ := splitBraceArg(rest)
			
			group := "default"
			if pendingMeta != nil {
				if g, ok := pendingMeta["group_name"].(string); ok && g != "" {
					group = g
				}
			}

			if name != "" && template != "" {
				ps.Aliases = append(ps.Aliases, AliasRule{
					Position: aliasPos, Name: name, Template: template, Enabled: true, GroupName: group,
				})
				aliasPos++
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#action ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#action "))
			pattern, rest := splitBraceArg(rest)
			command, rest := splitBraceArg(rest)
			
			// Compatibility with old exports: check if there are leftover brace arguments
			oldGroup, rest := splitBraceArg(rest)
			remaining := strings.TrimSpace(rest)

			isButton := false
			group := "default"

			if oldGroup != "" {
				group = oldGroup
			}
			if remaining == "button" || remaining == "{button}" {
				isButton = true
			}

			stopAfterMatch := false
			name := pattern
			if pendingMeta != nil {
				if pendingMeta["is_button"] == true {
					isButton = true
				}
				if pendingMeta["stop_after_match"] == true {
					stopAfterMatch = true
				}
				if n, ok := pendingMeta["name"].(string); ok && n != "" {
					name = n
				}
				if g, ok := pendingMeta["group_name"].(string); ok && g != "" {
					group = g
				}
			}

			if pattern != "" && command != "" {
				ps.Triggers = append(ps.Triggers, TriggerRule{
					Position: triggerPos, Name: name, Pattern: pattern, Command: command,
					IsButton: isButton, Enabled: true, StopAfterMatch: stopAfterMatch, GroupName: group,
				})
				triggerPos++
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#highlight ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#highlight "))
			fg, rest := splitBraceArg(rest)
			pattern, rest := splitBraceArg(rest)
			
			// Compatibility with old exports
			oldGroup, _ := splitBraceArg(rest)
			group := "default"
			if oldGroup != "" {
				group = oldGroup
			}

			if fg == "default" {
				fg = ""
			}

			if pendingMeta != nil {
				if g, ok := pendingMeta["group_name"].(string); ok && g != "" {
					group = g
				}
			}

			h := HighlightRule{
				Position: hlPos, Pattern: pattern, FG: fg, Enabled: true, GroupName: group,
			}
			if pendingMeta != nil {
				if b, ok := pendingMeta["bg"].(string); ok {
					h.BG = b
				}
				if b, ok := pendingMeta["bold"].(bool); ok {
					h.Bold = b
				}
				if b, ok := pendingMeta["faint"].(bool); ok {
					h.Faint = b
				}
				if b, ok := pendingMeta["italic"].(bool); ok {
					h.Italic = b
				}
				if b, ok := pendingMeta["underline"].(bool); ok {
					h.Underline = b
				}
				if b, ok := pendingMeta["strikethrough"].(bool); ok {
					h.Strikethrough = b
				}
				if b, ok := pendingMeta["blink"].(bool); ok {
					h.Blink = b
				}
				if b, ok := pendingMeta["reverse"].(bool); ok {
					h.Reverse = b
				}
			}

			if pattern != "" {
				ps.Highlights = append(ps.Highlights, h)
				hlPos++
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#hotkey ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#hotkey "))
			shortcut, rest := splitBraceArg(rest)
			command, _ := splitBraceArg(rest)
			if shortcut != "" && command != "" {
				ps.Hotkeys = append(ps.Hotkeys, HotkeyRule{
					Position: hkPos, Shortcut: shortcut, Command: command,
				})
				hkPos++
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#var ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#var "))
			name, rest := splitBraceArg(rest)
			defaultValue, _ := splitBraceArg(rest)

			if name != "" {
				dv := ProfileVariable{
					Position:     varPos,
					Name:         name,
					DefaultValue: defaultValue,
				}
				if pendingMeta != nil {
					if desc, ok := pendingMeta["description"].(string); ok {
						dv.Description = desc
					}
				}
				ps.DeclaredVariables = append(ps.DeclaredVariables, dv)
				varPos++
			}
			pendingMeta = nil
		}
	}
	return ps, nil
}

func (s *Store) ImportProfileScript(ps *ProfileScript) (Profile, error) {
	tx := s.db.Begin()
	if tx.Error != nil {
		return Profile{}, tx.Error
	}
	defer tx.Rollback()

	profileName := ps.Name
	if profileName == "" {
		profileName = "Imported Profile"
	}

	p := Profile{}
	err := tx.Where("name = ?", profileName).First(&p).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return Profile{}, err
		}
		p = Profile{
			Name:        profileName,
			Description: ps.Description,
			CreatedAt:   nowSQLiteTime(),
		}
		if err := tx.Create(&p).Error; err != nil {
			return Profile{}, err
		}
	} else {
		if err := tx.Model(&Profile{}).Where("id = ?", p.ID).Updates(map[string]any{
			"description": ps.Description,
		}).Error; err != nil {
			return Profile{}, err
		}
		if err := tx.Where("profile_id = ?", p.ID).Delete(&AliasRule{}).Error; err != nil {
			return Profile{}, err
		}
		if err := tx.Where("profile_id = ?", p.ID).Delete(&TriggerRule{}).Error; err != nil {
			return Profile{}, err
		}
		if err := tx.Where("profile_id = ?", p.ID).Delete(&HighlightRule{}).Error; err != nil {
			return Profile{}, err
		}
		if err := tx.Where("profile_id = ?", p.ID).Delete(&HotkeyRule{}).Error; err != nil {
			return Profile{}, err
		}
		if err := tx.Where("profile_id = ?", p.ID).Delete(&ProfileVariable{}).Error; err != nil {
			return Profile{}, err
		}
	}

	for _, dv := range ps.DeclaredVariables {
		dv.ProfileID = p.ID
		dv.UpdatedAt = nowSQLiteTime()
		if err := tx.Create(&dv).Error; err != nil {
			return Profile{}, err
		}
	}
	for _, a := range ps.Aliases {
		a.ProfileID = p.ID
		a.UpdatedAt = nowSQLiteTime()
		if err := tx.Create(&a).Error; err != nil {
			return Profile{}, err
		}
	}
	for _, t := range ps.Triggers {
		t.ProfileID = p.ID
		t.UpdatedAt = nowSQLiteTime()
		if err := tx.Create(&t).Error; err != nil {
			return Profile{}, err
		}
	}
	for _, h := range ps.Highlights {
		h.ProfileID = p.ID
		h.UpdatedAt = nowSQLiteTime()
		if err := tx.Create(&h).Error; err != nil {
			return Profile{}, err
		}
	}
	for _, hk := range ps.Hotkeys {
		hk.ProfileID = p.ID
		if err := tx.Create(&hk).Error; err != nil {
			return Profile{}, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return Profile{}, err
	}
	return p, nil
}

func SanitizeFilename(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9 _.-]`)
	return strings.TrimSpace(re.ReplaceAllString(name, "_"))
}
