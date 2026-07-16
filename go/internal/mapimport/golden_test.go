package mapimport

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
)

// Golden test against the real (private) corpus. Skips when the artifacts are
// absent so CI stays green without the private data.
const (
	goldenZip   = "/home/ru/rubymud/tmp/mapper-artifacts/rmud.zip"
	goldenIndex = "/home/ru/rubymud/tmp/mapper-artifacts/rmud_index.json"
)

// oracleRoom mirrors one entry of rmud_index.json. Fields the oracle omits
// (dx/dy/dl/fingerprint) are not represented here and not compared.
type oracleRoom struct {
	Zone       string   `json:"zone"`
	RI         int      `json:"ri"`
	Tag        *int     `json:"tag"`
	Hint       string   `json:"hint"`
	Desc       string   `json:"desc"`
	Exits      string   `json:"exits"`
	EDirs      []string `json:"edirs"`
	Doors      []string `json:"doors"`
	Ch         []string `json:"ch"`
	X          int      `json:"x"`
	Y          int      `json:"y"`
	L          int      `json:"l"`
	Note       string   `json:"note"`
	BColor     any      `json:"bcolor"`
	IsDT       bool     `json:"is_dt"`
	Pipe       bool     `json:"pipe"`
	ImageIndex *int     `json:"imageindex"`
	Automaps   []string `json:"automaps"`
}

func TestGoldenCorpus(t *testing.T) {
	zipData, err := os.ReadFile(goldenZip)
	if err != nil {
		t.Skipf("golden corpus absent (%v) — skipping", err)
	}
	idxData, err := os.ReadFile(goldenIndex)
	if err != nil {
		t.Skipf("golden index absent (%v) — skipping", err)
	}

	var oracle []oracleRoom
	if err := json.Unmarshal(idxData, &oracle); err != nil {
		t.Fatalf("parse oracle: %v", err)
	}

	pa, err := ParseZip(zipData, "rmud.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	got := pa.Rooms

	// Zone / room count invariants for this corpus.
	if pa.Summary.ZoneCount != 81 {
		t.Errorf("zone count = %d, want 81", pa.Summary.ZoneCount)
	}
	if len(got) != len(oracle) {
		t.Errorf("room count = %d, want %d", len(got), len(oracle))
	}
	if len(got) != 8034 {
		t.Errorf("room count = %d, want 8034", len(got))
	}

	// Compare ordered by (zone, ri). Both parser and oracle already emit zones in
	// sorted-filename order with dense ri, so index-aligned comparison also works;
	// we sort both defensively to be robust to ordering.
	sortByZoneRI := func(idx func(i int) (string, int), n int) []int {
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			za, ra := idx(order[a])
			zb, rb := idx(order[b])
			if za != zb {
				return za < zb
			}
			return ra < rb
		})
		return order
	}
	gotOrder := sortByZoneRI(func(i int) (string, int) { return got[i].Zone, got[i].RI }, len(got))
	oraOrder := sortByZoneRI(func(i int) (string, int) { return oracle[i].Zone, oracle[i].RI }, len(oracle))

	n := len(gotOrder)
	if len(oraOrder) < n {
		n = len(oraOrder)
	}
	mismatches := 0
	for k := 0; k < n; k++ {
		g := got[gotOrder[k]]
		o := oracle[oraOrder[k]]
		if diff := compareRoom(g, o); diff != "" {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("room[%s ri=%d]: %s", g.Zone, g.RI, diff)
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("total field mismatches: %d of %d rooms", mismatches, n)
	} else {
		t.Logf("golden OK: %d zones, %d rooms, all fields match", pa.Summary.ZoneCount, len(got))
	}
}

func compareRoom(g Room, o oracleRoom) string {
	if g.Zone != o.Zone {
		return fmt.Sprintf("zone %q != %q", g.Zone, o.Zone)
	}
	if g.RI != o.RI {
		return fmt.Sprintf("ri %d != %d", g.RI, o.RI)
	}
	if !intPtrEq(g.Tag, o.Tag) {
		return fmt.Sprintf("tag %v != %v", g.Tag, o.Tag)
	}
	if g.Hint != o.Hint {
		return fmt.Sprintf("hint %q != %q", g.Hint, o.Hint)
	}
	if g.Desc != o.Desc {
		return fmt.Sprintf("desc %q != %q", g.Desc, o.Desc)
	}
	if g.Exits != o.Exits {
		return fmt.Sprintf("exits %q != %q", g.Exits, o.Exits)
	}
	if !strSliceEq(g.EDirs, o.EDirs) {
		return fmt.Sprintf("edirs %v != %v", g.EDirs, o.EDirs)
	}
	if !strSliceEq(g.Doors, o.Doors) {
		return fmt.Sprintf("doors %v != %v", g.Doors, o.Doors)
	}
	if !strSliceEq(g.Ch, o.Ch) {
		return fmt.Sprintf("ch %v != %v", g.Ch, o.Ch)
	}
	if g.X != o.X || g.Y != o.Y || g.L != o.L {
		return fmt.Sprintf("coords (%d,%d,%d) != (%d,%d,%d)", g.X, g.Y, g.L, o.X, o.Y, o.L)
	}
	if g.Note != o.Note {
		return fmt.Sprintf("note %q != %q", g.Note, o.Note)
	}
	if !bcolorEq(g.BColor, o.BColor) {
		return fmt.Sprintf("bcolor %v (%T) != %v (%T)", g.BColor, g.BColor, o.BColor, o.BColor)
	}
	if g.IsDT != o.IsDT {
		return fmt.Sprintf("is_dt %v != %v", g.IsDT, o.IsDT)
	}
	if g.Pipe != o.Pipe {
		return fmt.Sprintf("pipe %v != %v", g.Pipe, o.Pipe)
	}
	if !intPtrEq(g.ImageIndex, o.ImageIndex) {
		return fmt.Sprintf("imageindex %v != %v", g.ImageIndex, o.ImageIndex)
	}
	if !strSliceEq(g.Automaps, o.Automaps) {
		return fmt.Sprintf("automaps %v != %v", g.Automaps, o.Automaps)
	}
	return ""
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func strSliceEq(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// bcolorEq compares BColor across the parser (int / string / nil) and the oracle
// (JSON decodes numbers as float64).
func bcolorEq(g, o any) bool {
	if g == nil || o == nil {
		return g == nil && o == nil
	}
	gn, gok := toFloat(g)
	on, ook := toFloat(o)
	if gok && ook {
		return gn == on
	}
	gs, gsok := g.(string)
	os_, osok := o.(string)
	if gsok && osok {
		return gs == os_
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}
