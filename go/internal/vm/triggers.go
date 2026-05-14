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

		expandedCmd := ExpandCaptures(ct.rule.Command, matches)
		if ct.rule.IsButton {
			label := expandedCmd
			runes := []rune(label)
			if len(runes) > 40 {
				label = string(runes[:37]) + "..."
			}
			effects = append(effects, Effect{Type: "button", Label: label, Command: expandedCmd, Captures: matches})
		} else {
			effects = append(effects, Effect{Type: "send", Command: ct.rule.Command, Captures: matches})
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
				routing.Echoes = append(routing.Echoes, EchoAction{TargetBuffer: ct.rule.TargetBuffer, Text: expandedCmd})
			}
		}

		if ct.rule.StopAfterMatch {
			break
		}
	}

	return effects, routing
}

func (v *VM) ApplyEffects(effects []Effect, entryID int64, buffer string, sendFn func(string, int64, string) error, echoFn func(Result)) []Effect {
	var buttons []Effect
	for _, e := range effects {
		switch e.Type {
		case "send":
			// Process trigger command through the full pipeline (variables, aliases, local commands)
			results := v.ProcessInputWithCaptures(e.Command, e.Captures)
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
