package session

import (
	"testing"
)

func TestSessionMCCPStats(t *testing.T) {
	s := &Session{}

	// Initial state
	active, comp, decomp, ratio := s.MCCPStats()
	if active {
		t.Error("expected inactive initially")
	}
	if comp != 0 || decomp != 0 {
		t.Errorf("expected zero counters, got %d/%d", comp, decomp)
	}
	if ratio != "0%" {
		t.Errorf("expected 0%% ratio, got %s", ratio)
	}

	// Active with some data
	s.mccpActive.Store(true)
	s.mccpCompressedBytes.Store(1000)
	s.mccpDecompressedBytes.Store(5000)

	active, comp, decomp, ratio = s.MCCPStats()
	if !active {
		t.Error("expected active")
	}
	if comp != 1000 || decomp != 5000 {
		t.Errorf("expected 1000/5000, got %d/%d", comp, decomp)
	}
	// (1 - 1000/5000) * 100 = 80%
	if ratio != "80.0%" {
		t.Errorf("expected 80.0%% ratio, got %s", ratio)
	}

	// Edge case: comp > decomp (should show 0% or handled gracefully)
	s.mccpCompressedBytes.Store(2000)
	s.mccpDecompressedBytes.Store(1000)
	_, _, _, ratio = s.MCCPStats()
	if ratio != "0%" {
		t.Errorf("expected 0%% for negative savings, got %s", ratio)
	}
}
