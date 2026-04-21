package vm

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"rubymud/go/internal/storage"
)

const maxExpandDepth = 10

type Effect struct {
	Type       string
	Command    string
	Label      string
	LogEntryID int64
}

type ResultKind string

const (
	ResultCommand ResultKind = "command"
	ResultEcho    ResultKind = "echo"
)

type Result struct {
	Text string
	Kind ResultKind
}

type VM struct {
	store      *storage.Store
	sessionID  int64
	aliases    []storage.AliasRule
	triggers   []storage.TriggerRule
	highlights []storage.HighlightRule
	variables  map[string]string
	varPattern *regexp.Regexp
}

func New(store *storage.Store, sessionID int64) *VM {
	return &VM{
		store:      store,
		sessionID:  sessionID,
		variables:  make(map[string]string),
		varPattern: regexp.MustCompile(`\$([\p{L}\p{N}_]+)`),
	}
}

func (v *VM) Reload() error {
	aliases, err := v.store.LoadAliases(v.sessionID)
	if err != nil {
		return err
	}
	v.aliases = aliases

	variables, err := v.store.LoadVariables(v.sessionID)
	if err != nil {
		return err
	}
	v.variables = variables

	triggers, err := v.store.LoadTriggers(v.sessionID)
	if err != nil {
		return err
	}
	v.triggers = triggers

	highlights, err := v.store.LoadHighlights(v.sessionID)
	if err != nil {
		return err
	}
	v.highlights = highlights
	return nil
}

func (v *VM) Aliases() []storage.AliasRule        { return v.aliases }
func (v *VM) Variables() map[string]string        { return v.variables }
func (v *VM) Triggers() []storage.TriggerRule     { return v.triggers }
func (v *VM) Highlights() []storage.HighlightRule { return v.highlights }

func (v *VM) ProcessInput(input string) []string {
	results := v.ProcessInputDetailed(input)
	output := make([]string, 0, len(results))
	for _, result := range results {
		output = append(output, result.Text)
	}
	return output
}

func (v *VM) ProcessInputDetailed(input string) []Result {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	if strings.HasPrefix(input, "#") {
		return v.dispatchCommand(input)
	}

	if expanded, ok := expandSpeedwalk(input); ok {
		return commandResults(expanded)
	}

	return commandResults(v.ExpandInput(input))
}

func (v *VM) dispatchCommand(input string) []Result {
	cmd := strings.TrimPrefix(input, "#")
	cmd = strings.TrimSpace(cmd)

	if cmd == "" {
		return nil
	}

	if cmd == "nop" || strings.HasPrefix(cmd, "nop ") {
		return nil
	}

	if numRepeat(cmd) > 0 {
		n, rest := parseRepeat(cmd)
		expanded := v.ExpandInput(rest)
		var result []string
		for i := 0; i < n; i++ {
			result = append(result, expanded...)
		}
		return commandResults(result)
	}

	keyword, args := splitFirstWord(cmd)
	rest := strings.Join(args, " ")

	switch keyword {
	case "alias", "ali":
		return v.cmdAlias(rest)
	case "unalias":
		return v.cmdUnalias(rest)
	case "variable", "var":
		return v.cmdVariable(rest)
	case "unvariable", "unvar":
		return v.cmdUnvariable(rest)
	case "action", "act":
		return v.cmdAction(rest)
	case "unaction", "unact":
		return v.cmdUnaction(rest)
	case "highlight", "high":
		return v.cmdHighlight(rest)
	case "unhighlight", "unhigh":
		return v.cmdUnhighlight(rest)
	}

	return []Result{{Text: input, Kind: ResultCommand}}
}

func (v *VM) cmdAlias(rest string) []Result {
	if rest == "" {
		var lines []string
		for _, a := range v.aliases {
			lines = append(lines, fmt.Sprintf("#alias {%s} {%s}", a.Name, a.Template))
		}
		if len(lines) == 0 {
			lines = append(lines, "#alias: no aliases defined")
		}
		return echoResults(lines)
	}

	name, afterName := splitBraceArg(rest)
	template, _ := splitBraceArg(strings.TrimSpace(afterName))
	if name == "" {
		return echoResults([]string{"#alias: usage: #alias {name} {template}"})
	}

	if template == "" {
		for _, a := range v.aliases {
			if a.Name == name {
				return echoResults([]string{fmt.Sprintf("#alias {%s} = {%s}", name, a.Template)})
			}
		}
		return echoResults([]string{fmt.Sprintf("#alias: %s not found", name)})
	}

	if v.store != nil {
		if err := v.store.SaveAlias(v.sessionID, name, template); err != nil {
			return echoResults([]string{fmt.Sprintf("#alias: save error: %v", err)})
		}
		v.Reload()
	} else {
		v.aliases = append(v.aliases, storage.AliasRule{Name: name, Template: template, Enabled: true})
	}
	return echoResults([]string{fmt.Sprintf("#alias {%s} = {%s}", name, template)})
}

func (v *VM) cmdUnalias(rest string) []Result {
	name := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if name == "" {
		return echoResults([]string{"#unalias: usage: #unalias {name}"})
	}
	if err := v.store.DeleteAlias(v.sessionID, name); err != nil {
		return echoResults([]string{fmt.Sprintf("#unalias: error: %v", err)})
	}
	v.Reload()
	return echoResults([]string{fmt.Sprintf("#unalias: %s removed", name)})
}

func (v *VM) cmdVariable(rest string) []Result {
	if rest == "" {
		var lines []string
		for k, val := range v.variables {
			lines = append(lines, fmt.Sprintf("#variable {%s} = {%s}", k, val))
		}
		if len(lines) == 0 {
			lines = append(lines, "#variable: no variables defined")
		}
		return echoResults(lines)
	}

	name, afterName := splitBraceArg(rest)
	value, _ := splitBraceArg(strings.TrimSpace(afterName))
	if name == "" {
		return echoResults([]string{"#variable: usage: #variable {name} [value]"})
	}

	if value == "" {
		if val, ok := v.variables[name]; ok {
			return echoResults([]string{fmt.Sprintf("#variable {%s} = {%s}", name, val)})
		}
		return echoResults([]string{fmt.Sprintf("#variable: %s not found", name)})
	}

	if v.store != nil {
		if err := v.store.SetVariable(v.sessionID, name, value); err != nil {
			return echoResults([]string{fmt.Sprintf("#variable: save error: %v", err)})
		}
	}
	v.variables[name] = value
	return echoResults([]string{fmt.Sprintf("#variable {%s} = {%s}", name, value)})
}

func (v *VM) cmdUnvariable(rest string) []Result {
	name := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if name == "" {
		return echoResults([]string{"#unvariable: usage: #unvariable {name}"})
	}
	if err := v.store.DeleteVariable(v.sessionID, name); err != nil {
		return echoResults([]string{fmt.Sprintf("#unvariable: error: %v", err)})
	}
	delete(v.variables, name)
	return echoResults([]string{fmt.Sprintf("#unvariable: %s removed", name)})
}

func (v *VM) cmdAction(rest string) []Result {
	if rest == "" {
		var lines []string
		for _, t := range v.triggers {
			s := fmt.Sprintf("#action {%s} {%s}", t.Pattern, t.Command)
			if t.IsButton {
				s += " {button}"
			}
			s += fmt.Sprintf(" {%s}", t.GroupName)
			lines = append(lines, s)
		}
		if len(lines) == 0 {
			lines = append(lines, "#action: no actions defined")
		}
		return echoResults(lines)
	}

	pattern, afterPattern := splitBraceArg(rest)
	command, afterCommand := splitBraceArg(afterPattern)
	group, afterGroup := splitBraceArg(afterCommand)

	isButton := false
	remaining := strings.TrimSpace(afterGroup)
	if remaining == "button" || strings.HasPrefix(remaining, "{button}") {
		isButton = true
	}

	if pattern == "" || command == "" {
		return echoResults([]string{"#action: usage: #action {pattern} {command} [group] [button]"})
	}

	if group == "" {
		group = "default"
	}

	if err := v.store.SaveTrigger(v.sessionID, pattern, command, isButton, group); err != nil {
		return echoResults([]string{fmt.Sprintf("#action: save error: %v", err)})
	}
	v.Reload()
	label := ""
	if isButton {
		label = " {button}"
	}
	return echoResults([]string{fmt.Sprintf("#action {%s} {%s} {%s}%s", pattern, command, group, label)})
}

func (v *VM) cmdUnaction(rest string) []Result {
	pattern := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if pattern == "" {
		return echoResults([]string{"#unaction: usage: #unaction {pattern}"})
	}
	if err := v.store.DeleteTrigger(v.sessionID, pattern); err != nil {
		return echoResults([]string{fmt.Sprintf("#unaction: error: %v", err)})
	}
	v.Reload()
	return echoResults([]string{fmt.Sprintf("#unaction: %s removed", pattern)})
}

func (v *VM) cmdHighlight(rest string) []Result {
	if rest == "" {
		var lines []string
		for _, h := range v.highlights {
			lines = append(lines, formatHighlight(h))
		}
		if len(lines) == 0 {
			lines = append(lines, "#highlight: no highlights defined")
		}
		return echoResults(lines)
	}

	colorSpec, afterColor := splitBraceArg(rest)
	pattern, afterPattern := splitBraceArg(strings.TrimSpace(afterColor))
	group, afterGroup := splitBraceArg(strings.TrimSpace(afterPattern))
	_ = afterGroup

	if pattern == "" {
		return echoResults([]string{"#highlight: usage: #highlight {color} {pattern} [group]"})
	}
	if group == "" {
		group = "default"
	}

	h := parseColorSpec(colorSpec)
	h.Pattern = pattern
	h.GroupName = group

	if v.store != nil {
		if err := v.store.SaveHighlight(v.sessionID, h); err != nil {
			return echoResults([]string{fmt.Sprintf("#highlight: save error: %v", err)})
		}
		v.Reload()
	} else {
		h.Enabled = true
		v.highlights = append(v.highlights, h)
	}
	return echoResults([]string{formatHighlight(h)})
}

func (v *VM) cmdUnhighlight(rest string) []Result {
	pattern := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if pattern == "" {
		return echoResults([]string{"#unhighlight: usage: #unhighlight {pattern}"})
	}
	if v.store != nil {
		if err := v.store.DeleteHighlight(v.sessionID, pattern); err != nil {
			return echoResults([]string{fmt.Sprintf("#unhighlight: error: %v", err)})
		}
		v.Reload()
	}
	return echoResults([]string{fmt.Sprintf("#unhighlight: %s removed", pattern)})
}

func formatHighlight(h storage.HighlightRule) string {
	color := formatColorSpec(h)
	s := fmt.Sprintf("#highlight {%s} {%s} {%s}", color, h.Pattern, h.GroupName)
	return s
}

func formatColorSpec(h storage.HighlightRule) string {
	parts := []string{}
	if h.FG != "" {
		parts = append(parts, h.FG)
	}
	if h.BG != "" {
		parts = append(parts, "b "+h.BG)
	}
	if h.Bold {
		parts = append(parts, "bold")
	}
	if h.Faint {
		parts = append(parts, "faint")
	}
	if h.Italic {
		parts = append(parts, "italic")
	}
	if h.Underline {
		parts = append(parts, "underline")
	}
	if h.Strikethrough {
		parts = append(parts, "strikethrough")
	}
	if h.Blink {
		parts = append(parts, "blink")
	}
	if h.Reverse {
		parts = append(parts, "reverse")
	}
	return strings.Join(parts, " ")
}

func parseColorSpec(spec string) storage.HighlightRule {
	var h storage.HighlightRule
	tokens := strings.Fields(spec)
	i := 0
	for i < len(tokens) {
		t := tokens[i]
		switch t {
		case "bold":
			h.Bold = true
		case "faint":
			h.Faint = true
		case "italic":
			h.Italic = true
		case "underline":
			h.Underline = true
		case "strikethrough":
			h.Strikethrough = true
		case "blink":
			h.Blink = true
		case "reverse":
			h.Reverse = true
		case "b":
			i++
			if i < len(tokens) {
				h.BG = tokens[i]
			}
		default:
			if h.FG == "" {
				h.FG = t
			}
		}
		i++
	}
	return h
}

func (v *VM) ApplyHighlights(text string) string {
	plainText := stripANSIFromVM(text)
	for i := range v.highlights {
		h := &v.highlights[i]
		if !h.Enabled {
			continue
		}
		re, err := regexp.Compile(h.Pattern)
		if err != nil {
			continue
		}
		loc := re.FindStringIndex(plainText)
		if loc == nil {
			continue
		}
		rawStart, rawEnd, ok := plainRangeToRawRange(text, loc[0], loc[1])
		if !ok || rawStart < 0 || rawEnd > len(text) || rawStart >= rawEnd {
			continue
		}
		matched := text[rawStart:rawEnd]
		ansi := highlightToANSI(h)
		text = text[:rawStart] + ansi + matched + resetANSI() + text[rawEnd:]
	}
	return text
}

func highlightToANSI(h *storage.HighlightRule) string {
	codes := []string{}
	fgMap := map[string]string{
		"black": "30", "red": "31", "green": "32", "brown": "33", "yellow": "33",
		"blue": "34", "magenta": "35", "cyan": "36", "white": "37", "gray": "37",
		"coal":      "30",
		"light red": "91", "light green": "92", "light yellow": "93",
		"light blue": "94", "light magenta": "95", "light cyan": "96", "light white": "97",
		"grey": "37", "charcoal": "30", "light brown": "33",
		"purple": "35",
	}
	bgMap := map[string]string{
		"black": "40", "red": "41", "green": "42", "brown": "43", "yellow": "43",
		"blue": "44", "magenta": "45", "cyan": "46", "white": "47", "gray": "47",
		"coal":      "40",
		"light red": "101", "light green": "102", "light yellow": "103",
		"light blue": "104", "light magenta": "105", "light cyan": "106", "light white": "107",
		"grey": "47", "charcoal": "40", "light brown": "43",
		"purple": "45",
	}

	if h.Bold {
		codes = append(codes, "1")
	}
	if h.Faint {
		codes = append(codes, "2")
	}
	if h.Italic {
		codes = append(codes, "3")
	}
	if h.Underline {
		codes = append(codes, "4")
	}
	if h.Blink {
		codes = append(codes, "5")
	}
	if h.Reverse {
		codes = append(codes, "7")
	}
	if h.Strikethrough {
		codes = append(codes, "9")
	}
	if h.FG != "" {
		if code, ok := fgMap[h.FG]; ok {
			codes = append(codes, code)
		} else if strings.HasPrefix(h.FG, "rgb") {
			parts := strings.Split(strings.TrimPrefix(h.FG, "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("38;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(h.FG, "256:") {
			codes = append(codes, fmt.Sprintf("38;5;%s", strings.TrimPrefix(h.FG, "256:")))
		}
	}
	if h.BG != "" {
		if code, ok := bgMap[h.BG]; ok {
			codes = append(codes, code)
		} else if strings.HasPrefix(h.BG, "rgb") {
			parts := strings.Split(strings.TrimPrefix(h.BG, "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("48;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(h.BG, "256:") {
			codes = append(codes, fmt.Sprintf("48;5;%s", strings.TrimPrefix(h.BG, "256:")))
		}
	}

	if len(codes) == 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}

func resetANSI() string {
	return "\x1b[0m"
}

func stripANSIFromVM(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}

func (v *VM) ExpandInput(input string) []string {
	return v.expand(input, 0)
}

func (v *VM) expand(input string, depth int) []string {
	if depth >= maxExpandDepth {
		return []string{input}
	}

	segments := splitSemicolons(input)
	var result []string

	for _, segment := range segments {
		segment = v.substituteVars(segment)
		expanded := v.expandAlias(segment, depth)
		result = append(result, expanded...)
	}

	return result
}

func (v *VM) substituteVars(s string) string {
	s = v.varPattern.ReplaceAllStringFunc(s, func(match string) string {
		key := match[1:]
		if val, ok := v.variables[key]; ok {
			return val
		}
		if val, ok := builtinVar(key); ok {
			return val
		}
		return match
	})
	return s
}

func builtinVar(key string) (string, bool) {
	now := time.Now()
	switch key {
	case "DATE":
		return now.Format("02-01-2006"), true
	case "TIME":
		return now.Format("15:04:05"), true
	case "HOUR":
		return fmt.Sprintf("%02d", now.Hour()), true
	case "MINUTE":
		return fmt.Sprintf("%02d", now.Minute()), true
	case "SECOND":
		return fmt.Sprintf("%02d", now.Second()), true
	case "TIMESTAMP":
		return fmt.Sprintf("%d", now.Unix()), true
	}
	return "", false
}

func (v *VM) expandAlias(input string, depth int) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}

	cmd, args := splitFirstWord(trimmed)

	for i := range v.aliases {
		if v.aliases[i].Name == cmd {
			expanded := substituteTemplate(v.aliases[i].Template, args)
			return v.expand(expanded, depth+1)
		}
	}

	return []string{input}
}

func substituteTemplate(template string, args []string) string {
	allArgs := strings.Join(args, " ")

	r := regexp.MustCompile(`%(\d+)`)
	result := r.ReplaceAllStringFunc(template, func(match string) string {
		idx := 0
		for _, c := range match[1:] {
			idx = idx*10 + int(c-'0')
		}
		if idx == 0 {
			return allArgs
		}
		if idx >= 1 && idx <= len(args) {
			return args[idx-1]
		}
		return match
	})

	return result
}

func (v *VM) MatchTriggers(plainText string, logEntryID int64) []Effect {
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
			effects = append(effects, Effect{
				Type:       "button",
				Label:      label,
				Command:    cmd,
				LogEntryID: logEntryID,
			})
		} else {
			effects = append(effects, Effect{
				Type:    "send",
				Command: cmd,
			})
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

func expandSpeedwalk(input string) ([]string, bool) {
	dirSet := map[rune]bool{
		'n': true, 's': true, 'e': true, 'w': true,
		'u': true, 'd': true,
	}

	allDirs := true
	for _, c := range input {
		if !dirSet[c] && (c < '0' || c > '9') {
			allDirs = false
			break
		}
	}
	if !allDirs || len(input) == 0 {
		return nil, false
	}

	var commands []string
	count := 0
	for _, c := range input {
		if c >= '0' && c <= '9' {
			count = count*10 + int(c-'0')
			continue
		}
		if dirSet[c] {
			n := count
			if n == 0 {
				n = 1
			}
			if n > 100 {
				n = 100
			}
			for i := 0; i < n; i++ {
				commands = append(commands, string(c))
			}
			count = 0
		}
	}

	if len(commands) == 0 {
		return nil, false
	}
	return commands, true
}

func splitFirstWord(input string) (string, []string) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

func splitSemicolons(input string) []string {
	parts := strings.Split(input, ";")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func commandResults(lines []string) []Result {
	results := make([]Result, 0, len(lines))
	for _, line := range lines {
		results = append(results, Result{Text: line, Kind: ResultCommand})
	}
	return results
}

func echoResults(lines []string) []Result {
	results := make([]Result, 0, len(lines))
	for _, line := range lines {
		results = append(results, Result{Text: line, Kind: ResultEcho})
	}
	return results
}

func plainRangeToRawRange(raw string, startPlain, endPlain int) (int, int, bool) {
	plainOffset := 0
	rawStart := -1
	rawEnd := -1
	inEscape := false

	for i := 0; i < len(raw); {
		if !inEscape && raw[i] == 0x1b {
			inEscape = true
			i++
			continue
		}

		if inEscape {
			if (raw[i] >= 'a' && raw[i] <= 'z') || (raw[i] >= 'A' && raw[i] <= 'Z') {
				inEscape = false
			}
			i++
			continue
		}

		if plainOffset == startPlain && rawStart == -1 {
			rawStart = i
		}

		_, size := utf8.DecodeRuneInString(raw[i:])
		if size <= 0 {
			return 0, 0, false
		}
		i += size
		plainOffset += size

		if plainOffset == endPlain {
			rawEnd = i
			break
		}
	}

	if startPlain == endPlain {
		return 0, 0, false
	}
	if rawStart == -1 || rawEnd == -1 {
		return 0, 0, false
	}
	return rawStart, rawEnd, true
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
				inner := s[1:i]
				rest := strings.TrimSpace(s[i+1:])
				return inner, rest
			}
		}
	}

	return strings.Trim(s, string(delim)), ""
}

func numRepeat(cmd string) int {
	for i, c := range cmd {
		if c < '0' || c > '9' {
			if i == 0 {
				return 0
			}
			n := 0
			for _, d := range cmd[:i] {
				n = n*10 + int(d-'0')
			}
			if n >= 1 && n <= 100 {
				return n
			}
			return 0
		}
	}
	return 0
}

func parseRepeat(cmd string) (int, string) {
	for i, c := range cmd {
		if c < '0' || c > '9' {
			n := 0
			for _, d := range cmd[:i] {
				n = n*10 + int(d-'0')
			}
			if c == ' ' || c == '{' {
				return n, strings.TrimSpace(cmd[i:])
			}
			return 0, cmd
		}
	}
	return 0, cmd
}
