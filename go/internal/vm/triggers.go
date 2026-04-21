package vm

import (
	"log"
	"regexp"
)

func (v *VM) MatchTriggers(plainText string, logEntryID int64) []Effect {
	v.ensureFresh()

	var effects []Effect
	for i := range v.triggers {
		t := &v.triggers[i]
		if !t.Enabled {
			continue
		}

		re, err := regexp.Compile(t.Pattern)
		if err != nil {
			log.Printf("trigger pattern compile error %q: %v", t.Pattern, err)
			continue
		}

		matches := re.FindStringSubmatch(plainText)
		if matches == nil {
			continue
		}

		cmd := expandTriggerCommand(t.Command, matches)
		if t.IsButton {
			label := cmd
			if len(label) > 40 {
				label = label[:37] + "..."
			}
			effects = append(effects, Effect{Type: "button", Label: label, Command: cmd, LogEntryID: logEntryID})
		} else {
			effects = append(effects, Effect{Type: "send", Command: cmd})
		}

		if t.StopAfterMatch {
			break
		}
	}

	return effects
}

func expandTriggerCommand(template string, matches []string) string {
	r := regexp.MustCompile(`%(\d+)`)
	return r.ReplaceAllStringFunc(template, func(match string) string {
		idx := 0
		for _, c := range match[1:] {
			idx = idx*10 + int(c-'0')
		}
		if idx < len(matches) {
			return matches[idx]
		}
		return match
	})
}

func (v *VM) ApplyEffects(effects []Effect, sendFunc func(string) error) []Effect {
	var buttons []Effect
	for _, e := range effects {
		switch e.Type {
		case "send":
			commands := v.ExpandInput(e.Command)
			for _, cmd := range commands {
				if err := sendFunc(cmd); err != nil {
					log.Printf("trigger send error: %v", err)
				}
			}
		case "button":
			if err := v.store.AppendButtonOverlay(e.LogEntryID, e.Label, e.Command); err != nil {
				log.Printf("button overlay error: %v", err)
			}
			buttons = append(buttons, e)
		}
	}
	return buttons
}
