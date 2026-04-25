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

	v.ProcessInputDetailed("#tickat {3} {stand}")
	if m.subSec != 3 || m.subCmd != "stand" {
		t.Errorf("tickat failed: got sec=%d cmd=%q", m.subSec, m.subCmd)
	}

	v.ProcessInputDetailed("#untickat {3}")
	if m.unsubSec != 3 {
		t.Errorf("untickat failed: got sec=%d", m.unsubSec)
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
