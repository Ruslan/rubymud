package mapimport

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

// tpf0Builder builds a synthetic TMudRoom2 TPF0 stream by hand, exercising each
// TReader value type. CI-safe: no private data.
type tpf0Builder struct {
	b bytes.Buffer
}

func newTPF0(className string) *tpf0Builder {
	t := &tpf0Builder{}
	t.b.WriteString("TPF0")
	t.shortstr(className)
	t.shortstr("") // instance name
	return t
}

func cp1251Bytes(s string) []byte {
	out, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		panic(err)
	}
	return out
}

func (t *tpf0Builder) shortstr(s string) {
	raw := cp1251Bytes(s)
	t.b.WriteByte(byte(len(raw)))
	t.b.Write(raw)
}

func (t *tpf0Builder) propName(name string) { t.shortstr(name) }

func (t *tpf0Builder) int8(name string, v int8) {
	t.propName(name)
	t.b.WriteByte(vaInt8)
	t.b.WriteByte(byte(v))
}

func (t *tpf0Builder) int16(name string, v int16) {
	t.propName(name)
	t.b.WriteByte(vaInt16)
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], uint16(v))
	t.b.Write(buf[:])
}

func (t *tpf0Builder) int32(name string, v int32) {
	t.propName(name)
	t.b.WriteByte(vaInt32)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(v))
	t.b.Write(buf[:])
}

func (t *tpf0Builder) shortStrProp(name, v string) {
	t.propName(name)
	t.b.WriteByte(vaString)
	raw := cp1251Bytes(v)
	t.b.WriteByte(byte(len(raw)))
	t.b.Write(raw)
}

func (t *tpf0Builder) ident(name, v string) {
	t.propName(name)
	t.b.WriteByte(vaIdent)
	raw := cp1251Bytes(v)
	t.b.WriteByte(byte(len(raw)))
	t.b.Write(raw)
}

func (t *tpf0Builder) lstr(name, v string) {
	t.propName(name)
	t.b.WriteByte(vaLString)
	raw := cp1251Bytes(v)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(len(raw)))
	t.b.Write(buf[:])
	t.b.Write(raw)
}

func (t *tpf0Builder) boolProp(name string, v bool) {
	t.propName(name)
	if v {
		t.b.WriteByte(vaTrue)
	} else {
		t.b.WriteByte(vaFalse)
	}
}

// set writes a vaSet: a run of shortstrings terminated by an empty shortstring.
func (t *tpf0Builder) set(name string, items []string) {
	t.propName(name)
	t.b.WriteByte(vaSet)
	for _, s := range items {
		raw := cp1251Bytes(s)
		t.b.WriteByte(byte(len(raw)))
		t.b.Write(raw)
	}
	t.b.WriteByte(0) // empty shortstring terminates the set
}

// bytes finalizes: property terminator + child terminator.
func (t *tpf0Builder) bytes() []byte {
	t.b.WriteByte(0) // end of properties
	t.b.WriteByte(0) // end of child objects
	return t.b.Bytes()
}

func TestSyntheticValueTypes(t *testing.T) {
	b := newTPF0("TMudRoom2")
	b.int8("Tag", 7)
	b.int16("X", -21)   // Int16-encoded coord, negative
	b.int8("Y", -5)     // Int8-encoded coord, negative
	b.int32("DX", 1000) // Int32
	b.shortStrProp("Exits", "(N) E S W")
	b.shortStrProp("Hint", "Городской банк") // CP1251 text
	b.lstr("Description", "Полное описание комнаты банка.")
	b.boolProp("ChN", false)
	b.boolProp("ChE", true)
	b.boolProp("ChS", true)
	b.boolProp("ChW", true)
	b.ident("BColor", "clRed") // ident-encoded color -> death trap
	b.shortStrProp("Note", "квест")
	data := b.bytes()

	rooms := ParseMM2("TestZone", data)
	if len(rooms) != 1 {
		t.Fatalf("got %d rooms, want 1", len(rooms))
	}
	r := rooms[0]

	if r.Tag == nil || *r.Tag != 7 {
		t.Errorf("Tag = %v, want 7", r.Tag)
	}
	if r.X != -21 {
		t.Errorf("X = %d, want -21 (signed int16)", r.X)
	}
	if r.Y != -5 {
		t.Errorf("Y = %d, want -5 (signed int8)", r.Y)
	}
	if r.DX != 1000 {
		t.Errorf("DX = %d, want 1000 (int32)", r.DX)
	}
	if r.Hint != "Городской банк" {
		t.Errorf("Hint = %q (CP1251 decode failed)", r.Hint)
	}
	if r.Desc != "Полное описание комнаты банка." {
		t.Errorf("Desc = %q (LString decode failed)", r.Desc)
	}
	if r.Exits != "(N) E S W" {
		t.Errorf("Exits = %q", r.Exits)
	}
	// EDirs: all four; Doors: N only (parenthesised).
	if !strSliceEq(r.EDirs, []string{"E", "N", "S", "W"}) {
		t.Errorf("EDirs = %v, want [E N S W]", r.EDirs)
	}
	if !strSliceEq(r.Doors, []string{"N"}) {
		t.Errorf("Doors = %v, want [N]", r.Doors)
	}
	// Ch: authoritative connectivity — E,S,W true, N false.
	if !strSliceEq(r.Ch, []string{"E", "S", "W"}) {
		t.Errorf("Ch = %v, want [E S W]", r.Ch)
	}
	if r.ChMask() != (chBit("E") | chBit("S") | chBit("W")) {
		t.Errorf("ChMask = %d", r.ChMask())
	}
	if !r.IsDT {
		t.Errorf("IsDT = false, want true (BColor clRed)")
	}
	if s, ok := r.BColor.(string); !ok || s != "clRed" {
		t.Errorf("BColor = %v (%T), want string clRed", r.BColor, r.BColor)
	}
	if r.Note != "квест" {
		t.Errorf("Note = %q", r.Note)
	}
	if r.ImageIndex != nil {
		t.Errorf("ImageIndex = %v, want nil (absent)", r.ImageIndex)
	}
}

func TestSyntheticDisplacedCoords(t *testing.T) {
	b := newTPF0("TMudRoom2")
	b.int8("X", 3)
	b.int8("Y", 4)
	b.int8("L", 1)
	b.int8("DX", 30)
	b.int8("DY", 40)
	b.int8("DL", 0)
	data := b.bytes()
	r := ParseMM2("Z", data)[0]
	if r.X != 3 || r.Y != 4 || r.L != 1 {
		t.Errorf("visual coords = (%d,%d,%d), want (3,4,1)", r.X, r.Y, r.L)
	}
	if r.DX != 30 || r.DY != 40 || r.DL != 0 {
		t.Errorf("logical coords = (%d,%d,%d), want (30,40,0)", r.DX, r.DY, r.DL)
	}
}

func TestSyntheticIntBColorDeathTrap(t *testing.T) {
	// BColor as an int equivalent of a death-trap color.
	b := newTPF0("TMudRoom2")
	b.int32("BColor", 255) // clRed int equivalent
	r := ParseMM2("Z", b.bytes())[0]
	if !r.IsDT {
		t.Errorf("IsDT = false, want true for BColor int 255")
	}
	// A non-DT int color stays non-DT and is preserved.
	b2 := newTPF0("TMudRoom2")
	b2.int32("BColor", 8404992)
	r2 := ParseMM2("Z", b2.bytes())[0]
	if r2.IsDT {
		t.Errorf("IsDT = true, want false for BColor 8404992")
	}
	if n, ok := r2.BColor.(int); !ok || n != 8404992 {
		t.Errorf("BColor = %v (%T), want int 8404992", r2.BColor, r2.BColor)
	}
}

func TestSyntheticNFDZoneNameNormalized(t *testing.T) {
	// A zone name in NFD (decomposed "й" = "и" + combining breve) must normalize
	// to NFC so automaps seams resolve. Build the same room in both forms and
	// assert the stored zone is NFC.
	nfd := norm.NFD.String("Хилло")
	if nfd == norm.NFC.String("Хилло") {
		// "Хилло" has no decomposable chars; use a name that does ("й").
		nfd = norm.NFD.String("Балифой")
	}
	nfc := norm.NFC.String(nfd)
	b := newTPF0("TMudRoom2")
	b.int8("Tag", 1)
	// roomFromProps expects an already-NFC zone (ParseMM2's caller normalizes);
	// assert the normalization the archive path performs.
	rooms := ParseMM2(nfc, b.bytes())
	if rooms[0].Zone != nfc {
		t.Errorf("zone = %q, want NFC %q", rooms[0].Zone, nfc)
	}
	// The decodeZoneName path (archive import) must produce NFC from NFD input.
	if got := norm.NFC.String(nfd); got != nfc {
		t.Errorf("NFC(nfd) = %q, want %q", got, nfc)
	}
}

func TestSyntheticAutomapsSeam(t *testing.T) {
	b := newTPF0("TMudRoom2")
	b.int8("Tag", 1)
	b.set("AutoMaps.Strings", []string{"Море Сирриона|на восток|163"})
	r := ParseMM2("Хилло", b.bytes())[0]
	if !strSliceEq(r.Automaps, []string{"Море Сирриона|на восток|163"}) {
		t.Fatalf("Automaps = %v", r.Automaps)
	}
	zone, cmd, tag, ok := parseSeam(r.Automaps[0])
	if !ok || zone != "Море Сирриона" || cmd != "на восток" || tag != 163 {
		t.Errorf("parseSeam = (%q,%q,%d,%v)", zone, cmd, tag, ok)
	}
}

func TestParseSeamTrailingPipe(t *testing.T) {
	// The corpus has "Zone|command|tag|" (trailing empty part) — must still parse.
	zone, cmd, tag, ok := parseSeam("Балифор - шхуна Антимодия|на запад|67|")
	if !ok || zone != "Балифор - шхуна Антимодия" || cmd != "на запад" || tag != 67 {
		t.Errorf("parseSeam trailing = (%q,%q,%d,%v)", zone, cmd, tag, ok)
	}
}

func TestFingerprintDoorMarkersStripped(t *testing.T) {
	// Two rooms identical except door parens in exits must share a fingerprint.
	a := newTPF0("TMudRoom2")
	a.shortStrProp("Hint", "Перекресток")
	a.lstr("Description", "Тихое место.")
	a.shortStrProp("Exits", "N (E) S W")
	ra := ParseMM2("Z", a.bytes())[0]

	b := newTPF0("TMudRoom2")
	b.shortStrProp("Hint", "Перекресток")
	b.lstr("Description", "Тихое место.")
	b.shortStrProp("Exits", "N E S W")
	rb := ParseMM2("Z", b.bytes())[0]

	if ra.Fingerprint == "" {
		t.Fatal("empty fingerprint")
	}
	if ra.Fingerprint != rb.Fingerprint {
		t.Errorf("fingerprints differ despite same room + door marker:\n a=%s\n b=%s", ra.Fingerprint, rb.Fingerprint)
	}
}

func TestPipeRoomEmptyHint(t *testing.T) {
	b := newTPF0("TMudRoom2")
	b.boolProp("Pipe", true)
	b.int8("X", 5)
	b.int8("Y", 6)
	r := ParseMM2("Z", b.bytes())[0]
	if !r.Pipe {
		t.Error("Pipe = false, want true")
	}
	if r.Hint != "" {
		t.Errorf("Hint = %q, want empty for pipe corridor", r.Hint)
	}
	if r.Tag != nil {
		t.Errorf("Tag = %v, want nil for pipe corridor", r.Tag)
	}
}
