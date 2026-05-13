package vm

import (
	"log"
	"regexp"
)

var triggerCaptureRef = regexp.MustCompile(`%(\d+)`)

func (v *VM) MatchTriggers(plainText string) ([]Effect, RoutingInfo) {
	v.ensureFresh()

	var effects []Effect
	routing := RoutingInfo{TargetBuffer: "main"}

	for _, ct := range v.compiledTriggers {
		if !ct.rule.Enabled {
			continue
		}
		if ct.re == nil {
			continue
		}

		matches := ct.re.FindStringSubmatch(plainText)
		if matches == nil {
			continue
		}

		cmd := expandTriggerCommand(ct.rule.Command, matches)
		if ct.rule.IsButton {
			label := cmd
			runes := []rune(label)
			if len(runes) > 40 {
				label = string(runes[:37]) + "..."
			}
			effects = append(effects, Effect{Type: "button", Label: label, Command: cmd})
		} else {
			effects = append(effects, Effect{Type: "send", Command: cmd})
		}

		if ct.rule.TargetBuffer != "" {
			switch ct.rule.BufferAction {
			case "move":
				if routing.TargetBuffer == "main" {
					routing.TargetBuffer = ct.rule.TargetBuffer
				}
			case "copy":
				routing.CopyBuffers = append(routing.CopyBuffers, ct.rule.TargetBuffer)
			case "echo":
				routing.Echoes = append(routing.Echoes, EchoAction{TargetBuffer: ct.rule.TargetBuffer, Text: cmd})
			}
		}

		if ct.rule.StopAfterMatch {
			break
		}
	}

	return effects, routing
}

func expandTriggerCommand(template string, matches []string) string {
	return triggerCaptureRef.ReplaceAllStringFunc(template, func(match string) string {
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

func (v *VM) ApplyEffects(effects []Effect, entryID int64, buffer string, sendFn func(string, int64, string) error, echoFn func(Result)) []Effect {
	var buttons []Effect
	for _, e := range effects {
		switch e.Type {
		case "send":
			// Process trigger command through the full pipeline (variables, aliases, local commands)
			results := v.ProcessInputDetailed(e.Command)
			for _, res := range results {
				if res.Kind == ResultEcho {
					echoFn(res)
				} else {
					if err := sendFn(res.Text, entryID, buffer); err != nil {
						log.Printf("trigger send error: %v", err)
					}
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
