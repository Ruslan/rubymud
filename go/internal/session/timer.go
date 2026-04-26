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
	mu            sync.Mutex
}

type TimerSnapshot struct {
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	CycleMS    int       `json:"cycle_ms"`
	NextTickAt time.Time `json:"next_tick_at"`
	Icon       string    `json:"icon"`
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
	if !t.Enabled || t.Cycle <= 0 {
		return -1
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
	return TimerSnapshot{
		Name:       t.Name,
		Enabled:    t.Enabled,
		CycleMS:    t.CycleMS,
		NextTickAt: t.NextTickAt,
		Icon:       t.Icon,
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
	t.NextTickAt = time.Now().Add(t.Cycle)
	return true
}

func (t *Timer) Off() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.Enabled {
		return false
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
		return true
	}

	t.Cycle = cycle
	t.CycleMS = int(cycle.Milliseconds())
	t.Enabled = true
	t.NextTickAt = time.Now().Add(t.Cycle)
	return true
}

func (t *Timer) Size(cycle time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cycle < 0 {
		return false
	}

	t.Cycle = cycle
	t.CycleMS = int(cycle.Milliseconds())
	if cycle == 0 {
		t.Enabled = false
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
		return true
	}
	return false
}
