package vm

import (
	"strings"
	"testing"
)

type mockTimerCtrl struct {
	scheduledCmd string
	scheduledSec float64
	timerCmds    map[string]map[int]string
}

func (m *mockTimerCtrl) TickOn(name string)                   {}
func (m *mockTimerCtrl) TickOff(name string)                  {}
func (m *mockTimerCtrl) TickReset(name string)                {}
func (m *mockTimerCtrl) TickSet(name string, sec float64)     {}
func (m *mockTimerCtrl) TickAdjust(name string, sec float64)  {}
func (m *mockTimerCtrl) TickSize(name string, sec float64)    {}
func (m *mockTimerCtrl) GetTimerCycleSeconds(name string) int { return 60 }
func (m *mockTimerCtrl) TickMode(name, mode string)           {}
func (m *mockTimerCtrl) TickIcon(name, icon string)           {}
func (m *mockTimerCtrl) SubscribeTimer(name string, sec int, cmd string) {
	if m.timerCmds == nil {
		m.timerCmds = make(map[string]map[int]string)
	}
	if m.timerCmds[name] == nil {
		m.timerCmds[name] = make(map[int]string)
	}
	m.timerCmds[name][sec] = cmd
}
func (m *mockTimerCtrl) UnsubscribeTimer(name string, sec int) {
	if m.timerCmds != nil && m.timerCmds[name] != nil {
		delete(m.timerCmds[name], sec)
	}
}
func (m *mockTimerCtrl) ScheduleDelay(id string, sec float64, cmd string) error {
	m.scheduledCmd = cmd
	m.scheduledSec = sec
	return nil
}
func (m *mockTimerCtrl) CancelDelay(id string) {}

func TestRuntimeDefinitionsPreserveVars(t *testing.T) {
	v := New(nil, 0)
	mock := &mockTimerCtrl{}
	v.timerCtrl = mock
	v.variables["target"] = "orc"
	v.variables["bag"] = "sack"
	v.variables["msg"] = "hello"

	// 1. #action
	v.ProcessInputDetailed("#action {^ready$} {kill $target}")
	found := false
	for _, tr := range v.triggers {
		if tr.Pattern == "^ready$" {
			if tr.Command != "kill $target" {
				t.Errorf("Action command expected 'kill $target', got %q", tr.Command)
			}
			found = true
		}
	}
	if !found {
		t.Error("Action not found in triggers")
	}

	// 2. #hotkey
	// cmdHotkey returns echo results and saves to store if present.
	// Since we don't have store, it only returns echo. We check echo text.
	res := v.ProcessInputDetailed("#hotkey {f1} {bash $target}")
	if len(res) != 1 || !strings.Contains(res[0].Text, "{bash $target}") {
		t.Errorf("Hotkey echo expected to contain '{bash $target}', got %v", res)
	}

	// 3. #tickat
	v.ProcessInputDetailed("#tickat {3} {wear $shield}")
	if cmd := mock.timerCmds["ticker"][3]; cmd != "wear $shield" {
		t.Errorf("Tickat command expected 'wear $shield', got %q", cmd)
	}

	// 4. #delay
	v.ProcessInputDetailed("#delay {1.5} {say $msg}")
	if mock.scheduledCmd != "say $msg" {
		t.Errorf("Delay command expected 'say $msg', got %q", mock.scheduledCmd)
	}

	// 5. #ticker
	v.ProcessInputDetailed("#ticker {herb} {58} {stand;use $herb}")
	if cmd := mock.timerCmds["herb"][0]; cmd != "stand;use $herb" {
		t.Errorf("Ticker command expected 'stand;use $herb', got %q", cmd)
	}

	// Regression 6: #showme
	res = v.ProcessInputDetailed("#showme {$target}")
	if len(res) != 1 || res[0].Text != "orc" {
		t.Errorf("Showme expected 'orc', got %q", res[0].Text)
	}

	// Regression 7: #var
	v.ProcessInputDetailed("#var {x} {$target}")
	if v.variables["x"] != "orc" {
		t.Errorf("Var expected 'orc', got %q", v.variables["x"])
	}

	// Regression 8: #alias (already tested in alias_dynamic_test.go, but good to have here too)
	v.ProcessInputDetailed("#alias {gt} {get %0 $bag}")
	got := v.ProcessInputDetailed("gt sword")
	if len(got) != 1 || got[0].Text != "get sword sack" {
		t.Errorf("Alias expected 'get sword sack', got %v", got)
	}

	// 9. #tickat with variables in metadata
	v.variables["sec"] = "3"
	v.ProcessInputDetailed("#tickat {$sec} {wear $shield}")
	if cmd := mock.timerCmds["ticker"][3]; cmd != "wear $shield" {
		t.Errorf("Dynamic tickat (2-arg) expected 'wear $shield' at 3, got %q", cmd)
	}

	v.variables["timer"] = "herb"
	v.variables["sec5"] = "5"
	v.ProcessInputDetailed("#tickat {$timer} {$sec5} {use $herb}")
	if cmd := mock.timerCmds["herb"][5]; cmd != "use $herb" {
		t.Errorf("Dynamic tickat (3-arg) expected 'use $herb' at 5 for 'herb', got %q", cmd)
	}

	// 10. #ticker with variables in metadata
	v.variables["seconds"] = "30"
	v.ProcessInputDetailed("#ticker {$timer} {$seconds} {drink $potion}")
	if cmd := mock.timerCmds["herb"][0]; cmd != "drink $potion" {
		t.Errorf("Dynamic ticker expected 'drink $potion', got %q", cmd)
	}

	// 11. #delay with variables in metadata
	v.variables["id"] = "task1"
	v.variables["wait"] = "2.5"
	v.ProcessInputDetailed("#delay {$id} {$wait} {echo done}")
	if mock.scheduledCmd != "echo done" || mock.scheduledSec != 2.5 {
		t.Errorf("Dynamic delay expected 'echo done' at 2.5, got %q at %v", mock.scheduledCmd, mock.scheduledSec)
	}
}
