package session

import (
	"math"
	"sync"
	"time"
)

type Timer struct {
	Name          string           `json:"name"`
	Enabled       bool             `json:"enabled"`
	Cycle         time.Duration    `json:"-"`
	CycleMS       int              `json:"cycle_ms"`
	NextTickAt    time.Time        `json:"next_tick_at"`
	Subscriptions map[int][]string `json:"-"`
	Icon          string           `json:"icon"`
	RemainingMS   int              `json:"remaining_ms"` // Paused or last known remaining time
	mu            sync.Mutex
}

type TimerSnapshot struct {
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	CycleMS     int       `json:"cycle_ms"`
	NextTickAt  time.Time `json:"next_tick_at"`
	Icon        string    `json:"icon"`
	RemainingMS int       `json:"remaining_ms"`
}

func NewTimer(name string, cycle time.Duration) *Timer {
	return &Timer{
		Name:          name,
		Cycle:         cycle,
		CycleMS:       int(cycle.Milliseconds()),
		Subscriptions: make(map[int][]string),
	}
}

func (t *Timer) RemainingSeconds() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Cycle <= 0 {
		return -1
	}
	if !t.Enabled {
		return int(math.Ceil(float64(t.RemainingMS) / 1000.0))
	}
	rem := time.Until(t.NextTickAt)
	if rem < 0 {
		return 0
	}
	return int(math.Ceil(rem.Seconds()))
}

func (t *Timer) CheckSubscriptions() (int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.Enabled || t.Cycle <= 0 {
		return -1, nil
	}

	rem := time.Until(t.NextTickAt)
	remSec := 0
	if rem > 0 {
		remSec = int(math.Ceil(rem.Seconds()))
	}

	if cmds, ok := t.Subscriptions[remSec]; ok {
		// Return a copy to avoid race on slice modification later
		copied := make([]string, len(cmds))
		copy(copied, cmds)
		return remSec, copied
	}

	return remSec, nil
}

func (t *Timer) Snapshot() TimerSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	remMS := t.RemainingMS
	if t.Enabled && t.Cycle > 0 {
		rem := time.Until(t.NextTickAt)
		if rem < 0 {
			remMS = 0
		} else {
			remMS = int(rem.Milliseconds())
		}
	}
	return TimerSnapshot{
		Name:        t.Name,
		Enabled:     t.Enabled,
		CycleMS:     t.CycleMS,
		NextTickAt:  t.NextTickAt,
		Icon:        t.Icon,
		RemainingMS: remMS,
	}
}

func (t *Timer) SetIcon(icon string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Icon = icon
}

func (t *Timer) On() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Enabled || t.Cycle <= 0 {
		return false
	}
	t.Enabled = true
	// Restore from RemainingMS if available, else start full cycle
	if t.RemainingMS > 0 {
		t.NextTickAt = time.Now().Add(time.Duration(t.RemainingMS) * time.Millisecond)
	} else {
		t.NextTickAt = time.Now().Add(t.Cycle)
	}
	return true
}

func (t *Timer) Off() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.Enabled {
		return false
	}
	// Store current remaining time before disabling
	rem := time.Until(t.NextTickAt)
	if rem < 0 {
		t.RemainingMS = 0
	} else {
		t.RemainingMS = int(rem.Milliseconds())
	}
	t.Enabled = false
	return true
}

func (t *Timer) Reset() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Cycle <= 0 {
		return false
	}
	t.Enabled = true
	t.NextTickAt = time.Now().Add(t.Cycle)
	t.RemainingMS = int(t.Cycle.Milliseconds())
	return true
}

func (t *Timer) Set(cycle time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cycle < 0 {
		return false
	}

	if cycle == 0 {
		t.Enabled = false
		t.Cycle = 0
		t.CycleMS = 0
		t.RemainingMS = 0
		return true
	}

	t.Cycle = cycle
	t.CycleMS = int(cycle.Milliseconds())
	t.Enabled = true
	t.NextTickAt = time.Now().Add(t.Cycle)
	t.RemainingMS = t.CycleMS
	return true
}

func (t *Timer) Size(cycle time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cycle < 0 {
		return false
	}

	oldCycle := t.Cycle
	t.Cycle = cycle
	t.CycleMS = int(cycle.Milliseconds())
	if cycle == 0 {
		t.Enabled = false
		t.RemainingMS = 0
	} else if !t.Enabled {
		// If disabled, try to preserve same relative remaining time if it was non-zero
		if oldCycle > 0 && t.RemainingMS > 0 {
			ratio := float64(t.RemainingMS) / float64(oldCycle.Milliseconds())
			t.RemainingMS = int(ratio * float64(t.CycleMS))
		}
	}
	return true
}

func (t *Timer) Adjust(deltaSeconds float64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.Cycle <= 0 {
		return false
	}

	deltaMS := int(deltaSeconds * 1000.0)

	if t.Enabled {
		newNextAt := t.NextTickAt.Add(time.Duration(deltaMS) * time.Millisecond)
		// Clamp: remaining must be between 0 and Cycle
		now := time.Now()
		rem := time.Until(newNextAt)
		if rem < 0 {
			t.NextTickAt = now
		} else if rem > t.Cycle {
			t.NextTickAt = now.Add(t.Cycle)
		} else {
			t.NextTickAt = newNextAt
		}
		// Sync RemainingMS for snapshot consistency
		t.RemainingMS = int(time.Until(t.NextTickAt).Milliseconds())
	} else {
		t.RemainingMS += deltaMS
		if t.RemainingMS < 0 {
			t.RemainingMS = 0
		} else if t.RemainingMS > t.CycleMS {
			t.RemainingMS = t.CycleMS
		}
	}

	return true
}

func (t *Timer) Check() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.Enabled || t.Cycle <= 0 {
		return false
	}
	if time.Now().After(t.NextTickAt) {
		t.NextTickAt = time.Now().Add(t.Cycle)
		// Update RemainingMS for snapshot consistency
		t.RemainingMS = t.CycleMS
		return true
	}
	return false
}
