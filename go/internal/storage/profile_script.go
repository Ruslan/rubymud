package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
	Timers            []ProfileTimer
	Subscriptions     []ProfileTimerSubscription
}

func splitBraceArg(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}

	delim := s[0]
	closer := byte('}')
	isQuoted := false
	switch delim {
	case '{':
		closer = '}'
	case '\'':
		closer = '\''
		isQuoted = true
	case '"':
		closer = '"'
		isQuoted = true
	default:
		parts := strings.SplitN(s, " ", 2)
		if len(parts) == 1 {
			return parts[0], ""
		}
		return parts[0], strings.TrimSpace(parts[1])
	}

	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == delim && !isQuoted {
			depth++
			continue
		}
		if s[i] == delim && isQuoted && i == 0 {
			depth = 1
			continue
		}
		if s[i] == closer {
			depth--
			if depth == 0 {
				return s[1:i], strings.TrimSpace(s[i+1:])
			}
		}
	}

	// Unclosed, return as is (fallback)
	return strings.Trim(s, string(delim)+string(closer)), ""
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
			if a.GroupName != "default" && a.GroupName != "" {
				meta := map[string]string{"group_name": a.GroupName}
				mj, _ := json.Marshal(meta)
				sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))
			}
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
			if t.GroupName != "default" && t.GroupName != "" {
				meta["group_name"] = t.GroupName
			}
			if t.TargetBuffer != "" {
				meta["target_buffer"] = t.TargetBuffer
			}
			if t.BufferAction != "" {
				meta["buffer_action"] = t.BufferAction
			}
			if len(meta) > 0 {
				mj, _ := json.Marshal(meta)
				sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))
			}
			sb.WriteString(fmt.Sprintf("#action {%s} {%s}\n", t.Pattern, t.Command))
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
			if h.GroupName != "default" && h.GroupName != "" {
				meta["group_name"] = h.GroupName
			}
			mj, _ := json.Marshal(meta)
			sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))

			fg := h.FG
			if fg == "" {
				fg = "default"
			}
			sb.WriteString(fmt.Sprintf("#highlight {%s} {%s}\n", fg, h.Pattern))
		}
		sb.WriteString("\n")
	}

	hotkeys, _ := s.ListHotkeys(profileID)
	if len(hotkeys) > 0 {
		for _, hk := range hotkeys {
			if hk.MobileRow != 0 || hk.MobileOrder != 0 {
				meta := map[string]int{"mobile_row": hk.MobileRow, "mobile_order": hk.MobileOrder}
				mj, _ := json.Marshal(meta)
				sb.WriteString(fmt.Sprintf("#nop rubymud:rule %s\n", string(mj)))
			}
			sb.WriteString(fmt.Sprintf("#hotkey {%s} {%s}\n", hk.Shortcut, hk.Command))
		}
		sb.WriteString("\n")
	}

	timers, _ := s.GetProfileTimers(profileID)
	if len(timers) > 0 {
		// Stable order by name, ticker first
		sort.Slice(timers, func(i, j int) bool {
			if timers[i].Name == "ticker" {
				return true
			}
			if timers[j].Name == "ticker" {
				return false
			}
			return timers[i].Name < timers[j].Name
		})

		for _, t := range timers {
			isDefault := t.Name == "ticker"

			if t.Icon != "" {
				if isDefault {
					sb.WriteString(fmt.Sprintf("#tickicon {%s}\n", t.Icon))
				} else {
					sb.WriteString(fmt.Sprintf("#tickicon {%s} {%s}\n", t.Name, t.Icon))
				}
			}
			if t.CycleMS > 0 {
				if isDefault {
					sb.WriteString(fmt.Sprintf("#ticksize {%g}\n", float64(t.CycleMS)/1000.0))
				} else {
					sb.WriteString(fmt.Sprintf("#ticksize {%s} {%g}\n", t.Name, float64(t.CycleMS)/1000.0))
				}
			}
			if t.RepeatMode == "one_shot" {
				if isDefault {
					sb.WriteString("#tickmode {one_shot}\n")
				} else {
					sb.WriteString(fmt.Sprintf("#tickmode {%s} {one_shot}\n", t.Name))
				}
			}
			subs, _ := s.GetProfileTimerSubscriptions(profileID, t.Name)
			for _, sub := range subs {
				if isDefault {
					sb.WriteString(fmt.Sprintf("#tickat {%d} {%s}\n", sub.Second, sub.Command))
				} else {
					sb.WriteString(fmt.Sprintf("#tickat {%s} {%d} {%s}\n", t.Name, sub.Second, sub.Command))
				}
			}
			sb.WriteString("\n")
		}
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

	ensurePSTimer := func(name string) *ProfileTimer {
		if name == "" {
			name = "ticker"
		}
		for i := range ps.Timers {
			if ps.Timers[i].Name == name {
				return &ps.Timers[i]
			}
		}
		ps.Timers = append(ps.Timers, ProfileTimer{
			Name:       name,
			CycleMS:    60000,
			RepeatMode: "repeating",
		})
		return &ps.Timers[len(ps.Timers)-1]
	}

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

			isButton := false
			stopAfterMatch := false
			group := "default"
			targetBuffer := ""
			bufferAction := ""
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
				if b, ok := pendingMeta["target_buffer"].(string); ok {
					targetBuffer = b
				}
				if a, ok := pendingMeta["buffer_action"].(string); ok {
					bufferAction = a
				}
			}

			// Support for legacy positional button/group args if meta is missing
			oldGroup, rest := splitBraceArg(rest)
			remaining := strings.TrimSpace(rest)
			if oldGroup != "" && (pendingMeta == nil || pendingMeta["group_name"] == nil) {
				group = oldGroup
			}
			if (remaining == "button" || remaining == "{button}") && (pendingMeta == nil || pendingMeta["is_button"] == nil) {
				isButton = true
			}

			if pattern != "" && command != "" {
				ps.Triggers = append(ps.Triggers, TriggerRule{
					Position: triggerPos, Name: name, Pattern: pattern, Command: command,
					IsButton: isButton, Enabled: true, StopAfterMatch: stopAfterMatch, GroupName: group,
					TargetBuffer: targetBuffer, BufferAction: bufferAction,
				})
				triggerPos++
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#highlight ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#highlight "))
			fg, rest := splitBraceArg(rest)
			pattern, rest := splitBraceArg(rest)

			group := "default"
			if fg == "default" {
				fg = ""
			}

			// Legacy positional group
			oldGroup, _ := splitBraceArg(rest)
			if oldGroup != "" {
				group = oldGroup
			}

			h := HighlightRule{
				Position: hlPos, Pattern: pattern, FG: fg, Enabled: true, GroupName: group,
			}
			if pendingMeta != nil {
				if g, ok := pendingMeta["group_name"].(string); ok && g != "" {
					h.GroupName = g
				}
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

			mRow := 0
			mOrder := 0
			if pendingMeta != nil {
				if r, ok := pendingMeta["mobile_row"].(float64); ok {
					mRow = int(r)
				}
				if o, ok := pendingMeta["mobile_order"].(float64); ok {
					mOrder = int(o)
				}
			}

			if shortcut != "" && command != "" {
				ps.Hotkeys = append(ps.Hotkeys, HotkeyRule{
					Position: hkPos, Shortcut: shortcut, Command: command,
					MobileRow: mRow, MobileOrder: mOrder,
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
		} else if strings.HasPrefix(line, "#tickicon ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#tickicon "))
			name, rest := splitBraceArg(rest)
			icon, _ := splitBraceArg(rest)
			if icon == "" {
				icon = name
				name = "ticker"
			}
			if name != "" {
				t := ensurePSTimer(name)
				t.Icon = icon
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#ticksize ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#ticksize "))
			name, rest := splitBraceArg(rest)
			secondsStr, _ := splitBraceArg(rest)
			if secondsStr == "" {
				secondsStr = name
				name = "ticker"
			}
			if name != "" {
				var s float64
				n, err := fmt.Sscanf(secondsStr, "%f", &s)
				if n != 1 || err != nil {
					return nil, fmt.Errorf("invalid #ticksize for %q: %q", name, secondsStr)
				}
				if s <= 0 {
					return nil, fmt.Errorf("invalid #ticksize for %q: cycle must be positive", name)
				}
				t := ensurePSTimer(name)
				t.CycleMS = int(s * 1000)
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#tickmode ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#tickmode "))
			name, rest := splitBraceArg(rest)
			mode, _ := splitBraceArg(rest)
			if mode == "" {
				mode = name
				name = "ticker"
			}
			if name != "" {
				if mode == "repeating" || mode == "one_shot" {
					t := ensurePSTimer(name)
					t.RepeatMode = mode
				} else {
					return nil, fmt.Errorf("invalid #tickmode for %q: %q", name, mode)
				}
			}
			pendingMeta = nil
		} else if strings.HasPrefix(line, "#tickat ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "#tickat "))
			name, rest := splitBraceArg(rest)
			secondStr, rest := splitBraceArg(rest)
			command, _ := splitBraceArg(rest)

			if command == "" {
				command = secondStr
				secondStr = name
				name = "ticker"
			}

			if name != "" && secondStr != "" && command != "" {
				var second int
				n, err := fmt.Sscanf(secondStr, "%d", &second)
				if n != 1 || err != nil {
					return nil, fmt.Errorf("invalid #tickat for %q: invalid second %q", name, secondStr)
				}

				// Add subscription
				ps.Subscriptions = append(ps.Subscriptions, ProfileTimerSubscription{
					TimerName: name,
					Second:    second,
					Command:   command,
				})
			}
			pendingMeta = nil
		}
	}

	// Final validation pass: ensure all subscriptions are within range of declared cycles
	for _, sub := range ps.Subscriptions {
		t := ensurePSTimer(sub.TimerName)
		maxSec := t.CycleMS / 1000
		if sub.Second < 0 || sub.Second > maxSec {
			return nil, fmt.Errorf("invalid #tickat for %q: second %d is out of range (max %d)", sub.TimerName, sub.Second, maxSec)
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
		if err := tx.Where("profile_id = ?", p.ID).Delete(&ProfileTimerSubscription{}).Error; err != nil {
			return Profile{}, err
		}
		if err := tx.Where("profile_id = ?", p.ID).Delete(&ProfileTimer{}).Error; err != nil {
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

	for _, t := range ps.Timers {
		t.ProfileID = p.ID
		if err := tx.Create(&t).Error; err != nil {
			return Profile{}, err
		}
	}
	// Track sort order per timer/second to allow multiple commands per second
	type timerSec struct {
		name   string
		second int
	}
	orders := make(map[timerSec]int)

	for _, sub := range ps.Subscriptions {
		sub.ProfileID = p.ID
		key := timerSec{sub.TimerName, sub.Second}
		sub.SortOrder = orders[key]
		orders[key]++

		if err := tx.Create(&sub).Error; err != nil {
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
