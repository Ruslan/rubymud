package mapper

import (
	"strings"
	"testing"
)

// fixtureStrip mimics the session's stripANSI (which processLine applies before
// handing plainText to the accumulator): removes ESC[...m sequences. It leaves CR
// for the accumulator's own TrimRight to handle.
func fixtureStrip(s string) string {
	var b strings.Builder
	data := []byte(s)
	in := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c == 0x1b {
			in = true
			continue
		}
		if in {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				in = false
			}
			continue
		}
		if c < 32 && c != '\t' && c != '\n' && c != '\r' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// feedBlock feeds each line of a block through the accumulator and returns the
// single emitted RoomEvent (fails if none/multiple).
func feedBlock(t *testing.T, acc *Accumulator, block string) RoomEvent {
	t.Helper()
	var got *RoomEvent
	for _, line := range strings.Split(block, "\n") {
		if ev, ok := acc.Feed(line); ok {
			e := ev
			got = &e
		}
	}
	if got == nil {
		t.Fatalf("no room event emitted for block:\n%s", block)
	}
	return *got
}

// Fixture 1 — doors all directions + down (following, empty queue).
const fixture1 = `Вы последовали за Ринтаом на север.
Кают-компания
..|..................      Вы стоите в большой, ярко освещенной каюте. Большой
.[ ]-[ ]-[ ]......... круглый стол на двенадцать персон приколочен к полу в центре
..|...|...|.......... комнаты, несколько столов поменьше расставлены по углам. Густой
.[ ]-[ ]-[ ]......... запах спирта мешает Вам дышать.
..|...|...|..........
.[v].[ ]-[v]-[ ].....
..|...|...|...|......
.[ ]-[v].[ ]-[ ].....
......|.......|......
.....[ ]-[v]-[ ].....
.....................
[ Exits: (N) (E) S (W) (D) ]`

// Fixture 2 — dead end none, brief desc.
const fixture2 = `Шалаш
..|.......|...|......      Это легкое сооружение из веток - место отдыха свободных от
-[ ].....[ ].[ ]..... стражи воинов. Ветви искусно сплетены между собой, так что
..|...........|...... этому шалашу нипочем и довольно сильный дождь. Несколько охапок
-[ ]-[ ]-[ ]-[ ]-[ ]- сена на полу приглашают Вас отдохнуть на них.
..............|...|..
.........[*].[ ]-[ ].
.....................
[ Exits: none ]`

// Fixture 3 — stairs up U, glyph [^].
const fixture3 = `Постоялый двор "У Аджойса"
..|..................      В помещении постоялого двора довольно многолюдно. В
.[ ]................. просторном зале установлено несколько больших деревянных
..|.................. столов, за которыми многочисленные путешественники едят и пьют,
.[ ]-[ ]............. обсуждая свершившиеся и будущие сделки. Несколько людей, по
..|.................. виду - наемников, играют в кости в углу. Хозяин заведения, сам
.[ ].....[^]......... старик Аджойс, обозревает зал из-за барной стойки. С кухни за
..|.......|.......... его спиной доносятся вкусные запахи. Крутая лестница ведет на
-[ ]-[ ]-[ ]-[ ]..... второй этаж, где расположены комнаты постояльцев.
..|...|...|..........
.[ ]-[ ]-[ ].........
..|...|...|..........
[ Exits: S U ]`

func TestParserFixture1_DoorsAndDown(t *testing.T) {
	acc := NewAccumulator()
	ev := feedBlock(t, acc, fixture1)
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q, want Кают-компания", ev.Hint)
	}
	if ev.Exits != "(N) (E) S (W) (D)" {
		t.Errorf("exits = %q, want (N) (E) S (W) (D)", ev.Exits)
	}
	if !strings.Contains(ev.Desc, "ярко освещенной каюте") {
		t.Errorf("desc missing prose start: %q", ev.Desc)
	}
	if !strings.Contains(ev.Desc, "мешает Вам дышать") {
		t.Errorf("desc missing prose end: %q", ev.Desc)
	}
	if strings.Contains(ev.Desc, "[") || strings.Contains(ev.Desc, "|") {
		t.Errorf("desc still contains minimap glyphs: %q", ev.Desc)
	}
}

func TestParserFixture2_NoneBrief(t *testing.T) {
	acc := NewAccumulator()
	ev := feedBlock(t, acc, fixture2)
	if ev.Hint != "Шалаш" {
		t.Errorf("hint = %q, want Шалаш", ev.Hint)
	}
	if ev.Exits != "none" {
		t.Errorf("exits = %q, want none", ev.Exits)
	}
	if !strings.Contains(ev.Desc, "легкое сооружение из веток") {
		t.Errorf("desc missing: %q", ev.Desc)
	}
}

func TestParserFixture3_StairsUp(t *testing.T) {
	acc := NewAccumulator()
	ev := feedBlock(t, acc, fixture3)
	if ev.Hint != `Постоялый двор "У Аджойса"` {
		t.Errorf("hint = %q", ev.Hint)
	}
	if ev.Exits != "S U" {
		t.Errorf("exits = %q, want S U", ev.Exits)
	}
	if !strings.Contains(ev.Desc, "постоялого двора довольно многолюдно") {
		t.Errorf("desc missing: %q", ev.Desc)
	}
}

func TestParserExitsGrammar(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"[ Exits: N E S W ]", "N E S W"},
		{"[ Exits: (N) E S W ]", "(N) E S W"},
		{"[ Exits: none ]", "none"},
		{"[ Exits: S U ]", "S U"},
		{"[ Exits: D ]", "D"},
		{"[ Exits: (N) (E) S (W) (D) ]", "(N) (E) S (W) (D)"},
	}
	for _, c := range cases {
		got, ok := ParseExits(c.line)
		if !ok {
			t.Errorf("ParseExits(%q) not recognized", c.line)
			continue
		}
		if got != c.want {
			t.Errorf("ParseExits(%q) = %q, want %q", c.line, got, c.want)
		}
	}
	// Non-exits lines must not match.
	for _, bad := range []string{"Кают-компания", "220ж 86б Выходы:ВЗ>", "[ Not exits ]"} {
		if _, ok := ParseExits(bad); ok {
			t.Errorf("ParseExits(%q) should not match", bad)
		}
	}
}

func TestParserANSIAndCRStripping(t *testing.T) {
	// The accumulator consumes ALREADY-stripped lines (processLine strips once and
	// hands plainText in). So we pre-strip each line with fixtureStrip — mirroring
	// the pipeline — and confirm the exits line's CR is trimmed by the accumulator.
	acc := NewAccumulator()
	rawLines := []string{
		"Тихая комната",
		".[ ].....      Простая комната.",
		"\x1b[0;36m[ Exits: N E S W ]\x1b[0;0m\r",
	}
	var ev RoomEvent
	var got bool
	for _, raw := range rawLines {
		if e, ok := acc.Feed(fixtureStrip(raw)); ok {
			ev, got = e, true
		}
	}
	if !got {
		t.Fatal("no room event emitted")
	}
	if ev.Hint != "Тихая комната" {
		t.Errorf("hint = %q", ev.Hint)
	}
	if ev.Exits != "N E S W" {
		t.Errorf("exits = %q, want N E S W (ANSI/CR stripped)", ev.Exits)
	}
}

func TestDirDeltas(t *testing.T) {
	cases := []struct {
		dir        string
		dx, dy, dl int
	}{
		{"N", -1, 0, 0},
		{"S", 1, 0, 0},
		{"E", 0, 1, 0},
		{"W", 0, -1, 0},
		{"U", 0, 0, 1},
		{"D", 0, 0, -1},
	}
	for _, c := range cases {
		d, ok := DirDelta(c.dir)
		if !ok {
			t.Fatalf("DirDelta(%q) missing", c.dir)
		}
		if d.DX != c.dx || d.DY != c.dy || d.DL != c.dl {
			t.Errorf("DirDelta(%q) = %+v, want {%d %d %d}", c.dir, d, c.dx, c.dy, c.dl)
		}
	}
}

func TestMoveDir(t *testing.T) {
	cases := map[string]string{
		"с": "N", "север": "N", "n": "N",
		"ю": "S", "юг": "S", "s": "S",
		"в": "E", "восток": "E", "e": "E",
		"з": "W", "запад": "W", "w": "W",
		"вв": "U", "вверх": "U", "подняться": "U", "u": "U",
		"вн": "D", "вниз": "D", "опуститься": "D", "d": "D",
	}
	for cmd, want := range cases {
		if d, ok := MoveDir(cmd); !ok || d != want {
			t.Errorf("MoveDir(%q) = %q,%v, want %q", cmd, d, ok, want)
		}
	}
	for _, bad := range []string{"", "look", "открыть дверь", "смотреть на север"} {
		if _, ok := MoveDir(bad); ok {
			t.Errorf("MoveDir(%q) should be false", bad)
		}
	}
}
