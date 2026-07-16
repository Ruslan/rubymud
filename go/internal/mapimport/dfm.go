// Package mapimport parses MUD Mapper (mm25x) .mm2 files — Delphi DFM/TPF0
// binary streams serializing TMudRoom2 objects (CP1251) — into rooms, and
// assembles a .zip archive of them into a single map set.
//
// The parser is a direct port of the reference Python parser (rmud_locate.py)
// plus the format notes in FINDINGS.md. Parsing must be pure and never block the
// MUD line pipeline (AGENTS.md #3): import runs off the hot path, on upload.
package mapimport

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/text/encoding/charmap"
)

// TValueType codes (Delphi TWriter/TReader). Only the subset that appears in
// TMudRoom2 streams is handled; the rest are skipped defensively.
const (
	vaNull       = 0x00
	vaList       = 0x01
	vaInt8       = 0x02
	vaInt16      = 0x03
	vaInt32      = 0x04
	vaExtended   = 0x05
	vaString     = 0x06 // ShortString (1-byte length)
	vaIdent      = 0x07
	vaFalse      = 0x08
	vaTrue       = 0x09
	vaBinary     = 0x0A
	vaSet        = 0x0B
	vaLString    = 0x0C // 4-byte length
	vaNil        = 0x0D
	vaCollection = 0x0E
	vaSingle     = 0x0F
	vaCurrency   = 0x10
	vaDate       = 0x11
	vaWString    = 0x12
	vaInt64      = 0x13
	vaUTF8       = 0x14
	vaDouble     = 0x15
)

var cp1251 = charmap.Windows1251

func decodeCP1251(b []byte) string {
	s, err := cp1251.NewDecoder().Bytes(b)
	if err != nil {
		// NewDecoder replaces invalid bytes; err is essentially never returned,
		// but fall back to a lossy latin decode rather than dropping the string.
		return string(b)
	}
	return string(s)
}

// reader walks a DFM byte stream. Bounds are checked so a corrupt/truncated
// stream returns an error instead of panicking (a bad .mm2 must not crash import).
type reader struct {
	d []byte
	i int
}

func (r *reader) remaining() int { return len(r.d) - r.i }

func (r *reader) peek() (byte, error) {
	if r.i >= len(r.d) {
		return 0, fmt.Errorf("unexpected EOF at %d", r.i)
	}
	return r.d[r.i], nil
}

func (r *reader) u8() (byte, error) {
	if r.i >= len(r.d) {
		return 0, fmt.Errorf("unexpected EOF at %d", r.i)
	}
	v := r.d[r.i]
	r.i++
	return v, nil
}

func (r *reader) i8() (int, error) {
	v, err := r.u8()
	if err != nil {
		return 0, err
	}
	if v >= 128 {
		return int(v) - 256, nil
	}
	return int(v), nil
}

func (r *reader) i16() (int, error) {
	if r.i+2 > len(r.d) {
		return 0, fmt.Errorf("unexpected EOF reading int16 at %d", r.i)
	}
	v := int16(binary.LittleEndian.Uint16(r.d[r.i : r.i+2]))
	r.i += 2
	return int(v), nil
}

func (r *reader) i32() (int, error) {
	if r.i+4 > len(r.d) {
		return 0, fmt.Errorf("unexpected EOF reading int32 at %d", r.i)
	}
	v := int32(binary.LittleEndian.Uint32(r.d[r.i : r.i+4]))
	r.i += 4
	return int(v), nil
}

func (r *reader) i64() (int64, error) {
	if r.i+8 > len(r.d) {
		return 0, fmt.Errorf("unexpected EOF reading int64 at %d", r.i)
	}
	v := int64(binary.LittleEndian.Uint64(r.d[r.i : r.i+8]))
	r.i += 8
	return v, nil
}

func (r *reader) shortstr() (string, error) {
	n, err := r.u8()
	if err != nil {
		return "", err
	}
	if r.i+int(n) > len(r.d) {
		return "", fmt.Errorf("unexpected EOF reading shortstr(%d) at %d", n, r.i)
	}
	s := decodeCP1251(r.d[r.i : r.i+int(n)])
	r.i += int(n)
	return s, nil
}

func (r *reader) lstr() (string, error) {
	n, err := r.i32()
	if err != nil {
		return "", err
	}
	if n < 0 || r.i+n > len(r.d) {
		return "", fmt.Errorf("unexpected EOF reading lstr(%d) at %d", n, r.i)
	}
	s := decodeCP1251(r.d[r.i : r.i+n])
	r.i += n
	return s, nil
}

// value reads a single typed value. Returns one of: int, int64, string, bool,
// nil, []string (set), []any (list), or []map[string]any (collection).
func (r *reader) value() (any, error) {
	t, err := r.u8()
	if err != nil {
		return nil, err
	}
	switch t {
	case vaInt8:
		return r.i8()
	case vaInt16:
		return r.i16()
	case vaInt32:
		return r.i32()
	case vaInt64:
		return r.i64()
	case vaString, vaIdent:
		return r.shortstr()
	case vaLString, vaUTF8:
		return r.lstr()
	case vaWString:
		n, err := r.i32()
		if err != nil {
			return nil, err
		}
		if n < 0 || r.i+n*2 > len(r.d) {
			return nil, fmt.Errorf("unexpected EOF reading wstring(%d) at %d", n, r.i)
		}
		// Decode UTF-16 LE.
		u16 := make([]uint16, n)
		for k := 0; k < n; k++ {
			u16[k] = binary.LittleEndian.Uint16(r.d[r.i+k*2 : r.i+k*2+2])
		}
		r.i += n * 2
		return string(utf16Decode(u16)), nil
	case vaFalse:
		return false, nil
	case vaTrue:
		return true, nil
	case vaNil, vaNull:
		return nil, nil
	case vaExtended:
		if r.i+10 > len(r.d) {
			return nil, fmt.Errorf("unexpected EOF reading extended at %d", r.i)
		}
		r.i += 10
		return 0.0, nil
	case vaSingle:
		if r.i+4 > len(r.d) {
			return nil, fmt.Errorf("unexpected EOF reading single at %d", r.i)
		}
		r.i += 4
		return 0.0, nil
	case vaDouble, vaDate, vaCurrency:
		if r.i+8 > len(r.d) {
			return nil, fmt.Errorf("unexpected EOF reading double at %d", r.i)
		}
		r.i += 8
		return 0.0, nil
	case vaSet:
		var out []string
		for {
			s, err := r.shortstr()
			if err != nil {
				return nil, err
			}
			if s == "" {
				break
			}
			out = append(out, s)
		}
		return out, nil
	case vaList:
		var out []any
		for {
			b, err := r.peek()
			if err != nil {
				return nil, err
			}
			if b == 0 {
				break
			}
			v, err := r.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		r.i++ // consume terminating 0
		return out, nil
	case vaBinary:
		n, err := r.i32()
		if err != nil {
			return nil, err
		}
		if n < 0 || r.i+n > len(r.d) {
			return nil, fmt.Errorf("unexpected EOF reading binary(%d) at %d", n, r.i)
		}
		r.i += n
		return nil, nil
	case vaCollection:
		var out []map[string]any
		for {
			b, err := r.peek()
			if err != nil {
				return nil, err
			}
			if b == 0 {
				break
			}
			// Optional integer index marker before an item's properties.
			if b == vaInt8 || b == vaInt16 || b == vaInt32 {
				if _, err := r.value(); err != nil {
					return nil, err
				}
			}
			props := map[string]any{}
			for {
				pb, err := r.peek()
				if err != nil {
					return nil, err
				}
				if pb == 0 {
					break
				}
				name, err := r.shortstr()
				if err != nil {
					return nil, err
				}
				v, err := r.value()
				if err != nil {
					return nil, err
				}
				props[name] = v
			}
			r.i++ // consume item terminator
			out = append(out, props)
		}
		r.i++ // consume collection terminator
		return out, nil
	default:
		return nil, fmt.Errorf("unknown value type 0x%02x at %d", t, r.i-1)
	}
}

func utf16Decode(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) {
			c2 := u[i+1]
			if c2 >= 0xDC00 && c2 <= 0xDFFF {
				out = append(out, ((rune(c)-0xD800)<<10)+(rune(c2)-0xDC00)+0x10000)
				i++
				continue
			}
		}
		out = append(out, rune(c))
	}
	return out
}

// parseObject parses one TPF0 object, positioned immediately after the "TPF0"
// magic. Returns the property map (with the class name stored under "_class").
func parseObject(r *reader) (map[string]any, error) {
	class, err := r.shortstr() // 'TMudRoom2'
	if err != nil {
		return nil, err
	}
	if _, err := r.shortstr(); err != nil { // instance name (usually empty)
		return nil, err
	}
	props := map[string]any{}
	for {
		b, err := r.peek()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			break
		}
		name, err := r.shortstr()
		if err != nil {
			return nil, err
		}
		v, err := r.value()
		if err != nil {
			return nil, err
		}
		props[name] = v
	}
	r.i++ // end of properties
	// Child objects, terminated by 0.
	for {
		b, err := r.peek()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			break
		}
		if _, err := parseObject(r); err != nil {
			return nil, err
		}
	}
	r.i++
	props["_class"] = class
	return props, nil
}
