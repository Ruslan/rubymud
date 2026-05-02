package vm

import (
	"strings"
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
	subName string
	subSec int
	subCmd string
	icon string
	iconName string
	adjName string
	adjVal float64
}

func (m *mockTimerControl) TickOn(name string)          { m.on = name }
func (m *mockTimerControl) TickOff(name string)         { m.off = name }
func (m *mockTimerControl) TickReset(name string)       { m.reset = name }
func (m *mockTimerControl) TickSet(name string, s float64) { m.set = name; m.setVal = s }
func (m *mockTimerControl) TickSize(name string, s float64) { m.size = name; m.sizeVal = s }
func (m *mockTimerControl) TickIcon(name string, icon string)              { m.iconName = name; m.icon = icon }
func (m *mockTimerControl) TickAdjust(name string, delta float64)          { m.adjName = name; m.adjVal = delta }
func (m *mockTimerControl) TickMode(name string, mode string)              {}
func (m *mockTimerControl) SubscribeTimer(name string, second int, command string) {
	m.subName = name
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
	if m.cycle == 0 {
		return 60 // Mock uninitialized named timer
	}
	return m.cycle
}

func (m *mockTimerControl) clear() {
	m.on = ""; m.off = ""; m.reset = ""; m.set = ""; m.setVal = 0; m.size = ""; m.sizeVal = 0
	m.subName = ""; m.subSec = -1; m.subCmd = ""
	m.iconName = ""; m.icon = ""
	m.adjName = ""; m.adjVal = 0
}

func (m *mockTimerControl) hasAnyAction() bool {
	return m.on != "" || m.off != "" || m.reset != "" || m.set != "" || m.size != "" || m.subCmd != "" || m.iconName != "" || m.adjName != ""
}

func TestTickerCommandDiagnostics(t *testing.T) {
	v := New(nil, 1)
	m := &mockTimerControl{cycle: 0} // Start with 0 to test default fallback
	v.SetTimerControl(m)

	tests := []struct {
		input    string
		expected string
		action   string // "on", "off", "reset", "set", "size", "tickat", "tickicon" or ""
		name     string
		val      float64
		sec      int
		icon     string
	}{
		{"#tickon", "", "on", "ticker", 0, 0, ""},
		{"#tickon {herb}", "", "on", "herb", 0, 0, ""},
		{"#tickoff", "", "off", "ticker", 0, 0, ""},
		{"#tickoff {herb}", "", "off", "herb", 0, 0, ""},
		{"#ticksize", "#ticksize: usage: #ticksize [{name}] {seconds}", "", "", 0, 0, ""},
		{"#ticksize {10}", "", "size", "ticker", 10, 0, ""},
		{"#ticksize {herb} {30}", "", "size", "herb", 30, 0, ""},
		{"#ticksize {-1}", "#ticksize: invalid non-negative seconds \"-1\"", "", "", 0, 0, ""},
		{"#tickset", "", "reset", "ticker", 0, 0, ""},
		{"#tickset {45}", "", "set", "ticker", 45, 0, ""},
		{"#tickset {herb}", "", "reset", "herb", 0, 0, ""},
		{"#tickset {herb} {15}", "", "set", "herb", 15, 0, ""},
		{"#tickat {70} {stand}", "#tickat: second 70 is out of range (max 60 for timer \"ticker\")", "", "", 0, 0, ""},
		{"#tickat {3} {stand}", "", "tickat", "ticker", 0, 3, ""},
		{"#tickat {herb} {0} {use herb}", "", "tickat", "herb", 0, 0, ""},
		{"#tickicon {🕒}", "", "tickicon", "ticker", 0, 0, "🕒"},
		{"#tickicon {herb} {🪴}", "", "tickicon", "herb", 0, 0, "🪴"},
		{"#tickicon {herb} {}", "", "tickicon", "herb", 0, 0, ""},
		{"#tickset {+5}", "", "adjust", "ticker", 5, 0, ""},
		{"#tickset {herb} {-2.5}", "", "adjust", "herb", -2.5, 0, ""},
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
				if m.on != tt.name { t.Errorf("input %q: expected TickOn(%q), got %q", tt.input, tt.name, m.on) }
			case "off":
				if m.off != tt.name { t.Errorf("input %q: expected TickOff(%q), got %q", tt.input, tt.name, m.off) }
			case "reset":
				if m.reset != tt.name { t.Errorf("input %q: expected TickReset(%q), got %q", tt.input, tt.name, m.reset) }
			case "set":
				if m.set != tt.name || m.setVal != tt.val { 
					t.Errorf("input %q: expected TickSet(%q, %v), got %s(%v)", tt.input, tt.name, tt.val, m.set, m.setVal) 
				}
			case "size":
				if m.size != tt.name || m.sizeVal != tt.val { 
					t.Errorf("input %q: expected TickSize(%q, %v), got %s(%v)", tt.input, tt.name, tt.val, m.size, m.sizeVal) 
				}
			case "tickat":
				if m.subName != tt.name || m.subSec != tt.sec || m.subCmd == "" {
					t.Errorf("input %q: expected SubscribeTimer(%q, %d), got %q, %d, %q", tt.input, tt.name, tt.sec, m.subName, m.subSec, m.subCmd)
				}
			case "tickicon":
				if m.iconName != tt.name || m.icon != tt.icon {
					t.Errorf("input %q: expected TickIcon(%q, %q), got %q, %q", tt.input, tt.name, tt.icon, m.iconName, m.icon)
				}
			case "adjust":
				if m.adjName != tt.name || m.adjVal != tt.val {
					t.Errorf("input %q: expected TickAdjust(%q, %v), got %q, %v", tt.input, tt.name, tt.val, m.adjName, m.adjVal)
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

func TestTickerCommand(t *testing.T) {
	v := New(nil, 1)
	m := &mockTimerControl{cycle: 60}
	v.SetTimerControl(m)

	// Valid command
	m.clear()
	v.ProcessInputDetailed("#ticker {herb} {58} {stand}")
	if m.set != "herb" || m.setVal != 58 {
		t.Errorf("expected TickSet(herb, 58), got %s(%v)", m.set, m.setVal)
	}
	if m.subName != "herb" || m.subSec != 0 || m.subCmd != "stand" {
		t.Errorf("expected SubscribeTimer(herb, 0, stand), got %s, %d, %s", m.subName, m.subSec, m.subCmd)
	}
	if m.on != "herb" {
		t.Errorf("expected TickOn(herb), got %q", m.on)
	}

	// Missing args
	m.clear()
	results := v.ProcessInputDetailed("#ticker {herb} {58}")
	if len(results) == 0 || results[0].Kind != ResultEcho || !strings.Contains(results[0].Text, "usage") {
		t.Errorf("expected usage diagnostic for missing args, got %v", results)
	}

	// Invalid name
	m.clear()
	results = v.ProcessInputDetailed("#ticker {123} {58} {stand}")
	if len(results) == 0 || results[0].Kind != ResultEcho || !strings.Contains(results[0].Text, "invalid timer name") {
		t.Errorf("expected invalid name diagnostic, got %v", results)
	}

	// Invalid seconds
	m.clear()
	results = v.ProcessInputDetailed("#ticker {herb} {-1} {stand}")
	if len(results) == 0 || results[0].Kind != ResultEcho || !strings.Contains(results[0].Text, "invalid non-negative seconds") {
		t.Errorf("expected invalid seconds diagnostic, got %v", results)
	}
}

