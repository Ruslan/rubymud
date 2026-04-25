package session

import (
	"testing"
	"time"
)

func TestTimerLogic(t *testing.T) {
	// 1. Size(0) -> inactive
	tr := NewTimer("test", 10*time.Second)
	tr.On()
	if !tr.Enabled {
		t.Error("expected timer to be enabled")
	}
	tr.Size(0)
	if tr.Enabled {
		t.Error("expected timer to be disabled after Size(0)")
	}
	if tr.Cycle != 0 {
		t.Errorf("expected cycle 0, got %v", tr.Cycle)
	}

	// 2. On() with cycle 0 must not enable
	tr2 := NewTimer("test2", 0)
	tr2.On()
	if tr2.Enabled {
		t.Error("expected timer with cycle 0 to remain disabled after On()")
	}

	// 3. Set(0) -> inactive
	tr3 := NewTimer("test3", 30*time.Second)
	tr3.On()
	tr3.Set(0)
	if tr3.Enabled {
		t.Error("expected timer to be disabled after Set(0)")
	}
}
