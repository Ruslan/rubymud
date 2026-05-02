package session

import (
	"testing"
	"rubymud/go/internal/vm"
)

type mockTimerControl struct {
	on        string
	off       string
	reset     string
	set       string
	setVal    float64
	size      string
	sizeVal   float64
	icon      string
	iconName  string
	subName   string
	subSec    int
	subCmd    string
	unsubName string
	unsubSec  int
	delayId   string
	delaySec  float64
	delayCmd  string
	cancelId  string
}

func (m *mockTimerControl) TickOn(name string) { m.on = name }
func (m *mockTimerControl) TickOff(name string) { m.off = name }
func (m *mockTimerControl) TickReset(name string) { m.reset = name }
func (m *mockTimerControl) TickSet(name string, seconds float64) { m.set = name; m.setVal = seconds }
func (m *mockTimerControl) TickSize(name string, seconds float64) { m.size = name; m.sizeVal = seconds }
func (m *mockTimerControl) TickIcon(name string, icon string) { m.iconName = name; m.icon = icon }
func (m *mockTimerControl) TickAdjust(name string, delta float64) {}
func (m *mockTimerControl) TickMode(name string, mode string)    {}
func (m *mockTimerControl) SubscribeTimer(name string, second int, command string) {
	m.subName = name
	m.subSec = second
	m.subCmd = command
}
func (m *mockTimerControl) UnsubscribeTimer(name string, second int) {
	m.unsubName = name
	m.unsubSec = second
}
func (m *mockTimerControl) ScheduleDelay(id string, seconds float64, command string) error {
	m.delayId = id
	m.delaySec = seconds
	m.delayCmd = command
	return nil
}
func (m *mockTimerControl) CancelDelay(id string) { m.cancelId = id }
func (m *mockTimerControl) GetTimerCycleSeconds(name string) int {
	return 60 // Default for tests
}
func TestVMTimerCommands(t *testing.T) {
	m := &mockTimerControl{}
	v := vm.New(nil, 1)
	v.SetTimerControl(m)

	// 1. #tickat on uninitialized named timer
	v.ProcessInputDetailed("#tickat {herb} {10} {herbcmd}")
	if m.subName != "herb" || m.subSec != 10 || m.subCmd != "herbcmd" {
		t.Errorf("tickat on new timer failed: got name=%q sec=%d cmd=%q", m.subName, m.subSec, m.subCmd)
	}

	// 2. #tickicon named timer
	v.ProcessInputDetailed("#tickicon {herb} {🪴}")
	if m.iconName != "herb" || m.icon != "🪴" {
		t.Errorf("named tickicon failed: got name=%q icon=%q", m.iconName, m.icon)
	}

	// 3. #tickicon clear (empty brace)
	v.ProcessInputDetailed("#tickicon {herb} {}")
	if m.iconName != "herb" || m.icon != "" {
		t.Errorf("named tickicon clear failed: got name=%q icon=%q", m.iconName, m.icon)
	}

	// 4. #tickicon default ticker
	v.ProcessInputDetailed("#tickicon {🕒}")
	if m.iconName != "ticker" || m.icon != "🕒" {
		t.Errorf("default tickicon failed: got name=%q icon=%q", m.iconName, m.icon)
	}

	v.ProcessInputDetailed("#tickat {3} {stand}")
	if m.subName != "ticker" || m.subSec != 3 || m.subCmd != "stand" {
		t.Errorf("tickat failed: got name=%q sec=%d cmd=%q", m.subName, m.subSec, m.subCmd)
	}

	v.ProcessInputDetailed("#untickat {3}")
	if m.unsubName != "ticker" || m.unsubSec != 3 {
		t.Errorf("untickat failed: got name=%q sec=%d", m.unsubName, m.unsubSec)
	}

	v.ProcessInputDetailed("#untickat {herb} {0}")
	if m.unsubName != "herb" || m.unsubSec != 0 {
		t.Errorf("named untickat failed: got name=%q sec=%d", m.unsubName, m.unsubSec)
	}

	v.ProcessInputDetailed("#tickon {herb}")
	if m.on != "herb" {
		t.Errorf("named tickon failed: got %q", m.on)
	}

	v.ProcessInputDetailed("#tickoff {herb}")
	if m.off != "herb" {
		t.Errorf("named tickoff failed: got %q", m.off)
	}

	v.ProcessInputDetailed("#ticksize {herb} {30}")
	if m.size != "herb" || m.sizeVal != 30 {
		t.Errorf("named ticksize failed: got name=%q val=%v", m.size, m.sizeVal)
	}

	v.ProcessInputDetailed("#tickset {herb} {15}")
	if m.set != "herb" || m.setVal != 15 {
		t.Errorf("named tickset failed: got name=%q val=%v", m.set, m.setVal)
	}

	// Validation tests
	results := v.ProcessInputDetailed("#tickon {123}")
	if len(results) == 0 || results[0].Kind != vm.ResultEcho {
		t.Error("expected diagnostic for numeric timer name in #tickon")
	}

	v.ProcessInputDetailed("#delay {2.5} {bash}")
	if m.delaySec != 2.5 || m.delayCmd != "bash" {
		t.Errorf("delay failed: got sec=%v cmd=%q", m.delaySec, m.delayCmd)
	}

	v.ProcessInputDetailed("#delay {myid} {1} {kick}")
	if m.delayId != "myid" || m.delaySec != 1 || m.delayCmd != "kick" {
		t.Errorf("named delay failed: got id=%q sec=%v cmd=%q", m.delayId, m.delaySec, m.delayCmd)
	}

	v.ProcessInputDetailed("#undelay {myid}")
	if m.cancelId != "myid" {
		t.Errorf("undelay failed: got id=%q", m.cancelId)
	}
}
