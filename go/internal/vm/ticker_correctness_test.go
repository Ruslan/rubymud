package vm

import (
	"testing"
)

type mockTimerControl struct {
	on    string
	off   string
	reset string
	set   string
	setVal float64
	size  string
	sizeVal float64
	cycle int
	subSec int
	subCmd string
}

func (m *mockTimerControl) TickOn(name string)          { m.on = name }
func (m *mockTimerControl) TickOff(name string)         { m.off = name }
func (m *mockTimerControl) TickReset(name string)       { m.reset = name }
func (m *mockTimerControl) TickSet(name string, s float64) { m.set = name; m.setVal = s }
func (m *mockTimerControl) TickSize(name string, s float64) { m.size = name; m.sizeVal = s }
func (m *mockTimerControl) SubscribeTimer(name string, second int, command string) {
	m.subSec = second
	m.subCmd = command
}
func (m *mockTimerControl) UnsubscribeTimer(name string, second int)                {}
func (m *mockTimerControl) ScheduleDelay(id string, seconds float64, command string) error { return nil }
func (m *mockTimerControl) CancelDelay(id string)                                    {}
func (m *mockTimerControl) GetTimerCycleSeconds(name string) int {
	if name == "ticker" && m.cycle == 0 {
		return 60
	}
	return m.cycle
}

func (m *mockTimerControl) clear() {
	m.on = ""; m.off = ""; m.reset = ""; m.set = ""; m.setVal = 0; m.size = ""; m.sizeVal = 0
	m.subSec = -1; m.subCmd = ""
}

func (m *mockTimerControl) hasAnyAction() bool {
	return m.on != "" || m.off != "" || m.reset != "" || m.set != "" || m.size != "" || m.subCmd != ""
}

func TestTickerCommandDiagnostics(t *testing.T) {
	v := New(nil, 1)
	m := &mockTimerControl{cycle: 0} // Start with 0 to test default fallback
	v.SetTimerControl(m)

	tests := []struct {
		input    string
		expected string
		action   string // "on", "off", "reset", "set", "size", "tickat" or ""
		val      float64
		sec      int
	}{
		{"#tickon", "", "on", 0, 0},
		{"#tickon {junk}", "#tickon: usage: #tickon (accepts no arguments)", "", 0, 0},
		{"#tickoff", "", "off", 0, 0},
		{"#tickoff {junk}", "#tickoff: usage: #tickoff (accepts no arguments)", "", 0, 0},
		{"#ticksize", "#ticksize: usage: #ticksize {seconds}", "", 0, 0},
		{"#ticksize {10}", "", "size", 10, 0},
		{"#ticksize {10} {junk}", "#ticksize: usage: #ticksize {seconds}", "", 0, 0},
		{"#ticksize {-1}", "#ticksize: invalid non-negative seconds \"-1\"", "", 0, 0},
		{"#tickset", "", "reset", 0, 0},
		{"#tickset {45}", "", "set", 45, 0},
		{"#tickset {45} {junk}", "#tickset: too many arguments, usage: #tickset [{seconds}]", "", 0, 0},
		{"#tickset {-5}", "#tickset: invalid non-negative seconds \"-5\"", "", 0, 0},
		{"#tickat {70} {stand}", "#tickat: second 70 is out of range (max 60)", "", 0, 0},
		{"#tickat {3} {stand}", "", "tickat", 0, 3},
		{"#tickat {30} {stand}", "", "tickat", 0, 30},
	}

	for _, tt := range tests {
		m.clear()
		results := v.ProcessInputDetailed(tt.input)
		
		// 1. Verify diagnostics
		if tt.expected == "" {
			if len(results) != 0 && results[0].Kind == ResultEcho {
				t.Errorf("input %q: unexpected diagnostic %q", tt.input, results[0].Text)
			}
		} else {
			if len(results) == 0 || results[0].Text != tt.expected {
				t.Errorf("input %q: expected diagnostic %q, got %v", tt.input, tt.expected, results)
			}
		}

		// 2. Verify callback actions
		if tt.action == "" {
			if m.hasAnyAction() {
				t.Errorf("input %q: expected no action, but callback was invoked", tt.input)
			}
		} else {
			switch tt.action {
			case "on":
				if m.on != "ticker" { t.Errorf("input %q: expected TickOn, got nothing", tt.input) }
			case "off":
				if m.off != "ticker" { t.Errorf("input %q: expected TickOff, got nothing", tt.input) }
			case "reset":
				if m.reset != "ticker" { t.Errorf("input %q: expected TickReset, got nothing", tt.input) }
			case "set":
				if m.set != "ticker" || m.setVal != tt.val { 
					t.Errorf("input %q: expected TickSet(ticker, %v), got %s(%v)", tt.input, tt.val, m.set, m.setVal) 
				}
			case "size":
				if m.size != "ticker" || m.sizeVal != tt.val { 
					t.Errorf("input %q: expected TickSize(ticker, %v), got %s(%v)", tt.input, tt.val, m.size, m.sizeVal) 
				}
			case "tickat":
				if m.subSec != tt.sec || m.subCmd == "" {
					t.Errorf("input %q: expected SubscribeTimer at %d, got sec=%d cmd=%q", tt.input, tt.sec, m.subSec, m.subCmd)
				}
			}
		}
	}
}

func TestTTSAlias(t *testing.T) {
	var spoken string
	v := New(nil, 1)
	v.SetTTS(func(s string) { spoken = s })

	v.ProcessInputDetailed("#ts {hello}")
	if spoken != "hello" {
		t.Errorf("expected #ts to trigger TTS, got %q", spoken)
	}

	spoken = ""
	v.ProcessInputDetailed("#tts {world}")
	if spoken != "world" {
		t.Errorf("expected #tts to trigger TTS, got %q", spoken)
	}
}
