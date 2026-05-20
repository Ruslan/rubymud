//go:build race

package vm

import (
	"fmt"
	"sync"
	"testing"

	"rubymud/go/internal/storage"
)

func TestVMConcurrentRuntimeAccessRace(t *testing.T) {
	v := New(nil, 1)
	v.variables["target"] = "danger"
	v.substitutes = []storage.SubstituteRule{
		{ID: 1, Pattern: "$target", Replacement: "safe", Enabled: true},
	}
	v.highlights = []storage.HighlightRule{
		{Pattern: "$target", FG: "red", Enabled: true},
	}

	// Warm the one-time compiled caches so the test focuses on concurrent runtime
	// operations, not test setup.
	_ = v.ApplyHighlights("danger")

	start := make(chan struct{})
	var wg sync.WaitGroup

	run := func(fn func(int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 2000; i++ {
				fn(i)
			}
		}()
	}

	for i := 0; i < 4; i++ {
		run(func(int) {
			_, _, _ = v.ApplySubsAndCollectOverlays("danger zone", "danger zone")
		})
		run(func(int) {
			_ = v.ApplyHighlights("danger input")
		})
	}

	run(func(i int) {
		v.ProcessInputDetailed(fmt.Sprintf("#variable {target} {danger%d}", i%8))
	})

	close(start)
	wg.Wait()
}

func TestVMConcurrentReloadRace(t *testing.T) {
	store := newRuntimeTestStore(t)
	if err := store.SetVariable(1, "target", "danger"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	if err := store.SaveHighlight(1, storage.HighlightRule{Pattern: "$target", FG: "red", Enabled: true, GroupName: "default"}); err != nil {
		t.Fatalf("SaveHighlight: %v", err)
	}
	if err := store.SaveSubstitute(1, "$target", "safe", false, "default"); err != nil {
		t.Fatalf("SaveSubstitute: %v", err)
	}
	if err := store.SaveTrigger(1, `^danger`, "look", false, "default"); err != nil {
		t.Fatalf("SaveTrigger: %v", err)
	}

	v := New(store, 1)
	if err := v.ReloadFromStore(); err != nil {
		t.Fatalf("ReloadFromStore: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				fn()
			}
		}()
	}

	run(func() { _ = v.Reload() })
	run(func() { _ = v.ReloadFromStore() })
	run(func() { _, _ = v.MatchTriggers("danger") })
	run(func() { _, _, _ = v.ApplySubsAndCollectOverlays("danger", "danger") })
	run(func() { _ = v.ApplyHighlights("danger") })
	run(func() { _ = v.Variables() })
	run(func() { _ = v.Highlights() })
	run(func() { _ = v.Triggers() })

	close(start)
	wg.Wait()
}
