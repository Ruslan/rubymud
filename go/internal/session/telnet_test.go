package session

import (
	"bytes"
	"testing"
)

func TestTelnetDecoderPlainText(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("hello world"))
	if len(events) != 1 || events[0].typ != telEventText {
		t.Fatalf("expected 1 text event, got %v", events)
	}
	if string(events[0].data) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", events[0].data)
	}
}

func TestTelnetDecoderIAC_IAC(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC, telIAC})
	if len(events) != 1 || events[0].typ != telEventText {
		t.Fatalf("expected 1 text event, got %v", events)
	}
	if len(events[0].data) != 1 || events[0].data[0] != telIAC {
		t.Fatalf("expected single 0xFF byte, got %v", events[0].data)
	}
}

func TestTelnetDecoderIAC_IACBetweenText(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("A" + string([]byte{telIAC, telIAC}) + "B"))
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	var buf bytes.Buffer
	for _, ev := range events {
		if ev.typ != telEventText {
			t.Fatalf("expected only text events, got %v", ev.typ)
		}
		buf.Write(ev.data)
	}
	if buf.String() != "A\xffB" {
		t.Fatalf("expected 'A\\xffB', got %q", buf.String())
	}
}

func TestTelnetDecoderFlushOnGA(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("prompt>"))
	if len(events) != 1 || events[0].typ != telEventText {
		t.Fatalf("expected 1 text event before GA, got %v", events)
	}

	events2 := d.Feed([]byte{telIAC, telGA})
	if len(events2) != 1 || events2[0].typ != telEventFlush {
		t.Fatalf("expected flush event, got %v", events2)
	}
}

func TestTelnetDecoderFlushOnEOR(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("prompt>" + string([]byte{telIAC, telEOR})))
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].typ != telEventText || string(events[0].data) != "prompt>" {
		t.Fatalf("expected text 'prompt>', got %v", events[0])
	}
	if events[1].typ != telEventFlush {
		t.Fatalf("expected flush event, got %v", events[1])
	}
}

func TestTelnetDecoderWILL(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC, telWILL, 86})
	if len(events) != 1 || events[0].typ != telEventWill || events[0].opt != 86 {
		t.Fatalf("expected WILL 86 event, got %v", events)
	}
}

func TestTelnetDecoderDO_DONT_WONT(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantTyp telEventType
		wantOpt byte
	}{
		{"DO 24", []byte{telIAC, telDO, 24}, telEventDo, 24},
		{"DONT 24", []byte{telIAC, telDONT, 24}, telEventDont, 24},
		{"WONT 86", []byte{telIAC, telWONT, 86}, telEventWont, 86},
		{"WILL 86", []byte{telIAC, telWILL, 86}, telEventWill, 86},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newTelnetDecoder()
			events := d.Feed(tc.data)
			if len(events) != 1 || events[0].typ != tc.wantTyp || events[0].opt != tc.wantOpt {
				t.Fatalf("expected %v %d, got %v", tc.wantTyp, tc.wantOpt, events)
			}
		})
	}
}

func TestTelnetDecoderWILLFragmented(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC})
	if len(events) != 0 {
		t.Fatalf("expected 0 events after only IAC, got %d", len(events))
	}

	events2 := d.Feed([]byte{telWILL, 86})
	if len(events2) != 1 || events2[0].typ != telEventWill || events2[0].opt != 86 {
		t.Fatalf("expected WILL 86, got %v", events2)
	}
}

func TestTelnetDecoderSBFragmented(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC, telSB, 86})
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}

	events2 := d.Feed([]byte{telIAC, telSE})
	if len(events2) != 1 || events2[0].typ != telEventMCCP2Start {
		t.Fatalf("expected MCCP2Start event, got %v", events2)
	}
}

func TestTelnetDecoderMCCP2WithRemainingCompressed(t *testing.T) {
	d := newTelnetDecoder()
	// SB MCCP2 IAC SE followed by 5 bytes of compressed data
	data := []byte{telIAC, telSB, mccp2, telIAC, telSE, 0x01, 0x02, 0x03, 0x04, 0x05}
	events := d.Feed(data)
	if len(events) != 1 || events[0].typ != telEventMCCP2Start {
		t.Fatalf("expected MCCP2Start event, got %v", events)
	}
	remaining := d.RemainingCompressed()
	if len(remaining) != 5 || remaining[0] != 0x01 || remaining[4] != 0x05 {
		t.Fatalf("expected 5 compressed bytes, got %v", remaining)
	}
}

func TestTelnetDecoderOrderedStream(t *testing.T) {
	d := newTelnetDecoder()
	// Input: "A" IAC GA "B"
	data := []byte{'A', telIAC, telGA, 'B'}
	events := d.Feed(data)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(events), events)
	}
	if events[0].typ != telEventText || string(events[0].data) != "A" {
		t.Errorf("expected text A, got %v", events[0])
	}
	if events[1].typ != telEventFlush {
		t.Errorf("expected flush, got %v", events[1])
	}
	if events[2].typ != telEventText || string(events[2].data) != "B" {
		t.Errorf("expected text B, got %v", events[2])
	}
}

func TestTelnetDecoderUnknownWILL(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC, telWILL, 24})
	if len(events) != 1 || events[0].typ != telEventWill || events[0].opt != 24 {
		t.Fatalf("expected WILL 24 event, got %v", events)
	}
}

func TestTelnetDecoderTextAroundNegotiation(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("hello" + string([]byte{telIAC, telWILL, 86}) + "world"))
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].typ != telEventText || string(events[0].data) != "hello" {
		t.Errorf("expected text hello, got %v", events[0])
	}
	if events[1].typ != telEventWill || events[1].opt != 86 {
		t.Errorf("expected WILL 86, got %v", events[1])
	}
	if events[2].typ != telEventText || string(events[2].data) != "world" {
		t.Errorf("expected text world, got %v", events[2])
	}
}

func TestTelnetDecoderMultipleEvents(t *testing.T) {
	d := newTelnetDecoder()
	// Two WILLs: WILL 86, WILL 24
	events := d.Feed([]byte{telIAC, telWILL, 86, telIAC, telWILL, 24})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].typ != telEventWill || events[0].opt != 86 {
		t.Fatalf("first event should be WILL 86, got %v", events[0])
	}
	if events[1].typ != telEventWill || events[1].opt != 24 {
		t.Fatalf("second event should be WILL 24, got %v", events[1])
	}
}

func TestTelnetDecoderNonMCCP2Subneg(t *testing.T) {
	d := newTelnetDecoder()
	// SB 24 (unknown opt) IAC SE
	events := d.Feed([]byte{telIAC, telSB, 24, 0x01, 0x02, telIAC, telSE})
	if len(events) != 1 || events[0].typ != telEventSB || events[0].opt != 24 {
		t.Fatalf("expected SB 24 event, got %v", events)
	}
}

func TestTelnetDecoderFlushInMiddleOfData(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("line1\nline2"))
	if len(events) != 1 || events[0].typ != telEventText || string(events[0].data) != "line1\nline2" {
		t.Fatalf("expected 1 text event with newline, got %v", events)
	}
}

func TestTelnetDecoderMixedTextAndFlush(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte("prompt>" + string([]byte{telIAC, telGA})))
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].typ != telEventText || string(events[0].data) != "prompt>" {
		t.Fatalf("expected text prompt>, got %v", events[0])
	}
	if events[1].typ != telEventFlush {
		t.Fatalf("expected 1 flush event, got %v", events[1])
	}
}

func TestTelnetDecoderIACThenText(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC})
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}

	events2 := d.Feed([]byte("hello"))
	if len(events2) != 1 || events2[0].typ != telEventText || string(events2[0].data) != "ello" {
		t.Fatalf("expected text 'ello' (after consuming 'h' as invalid command), got %v", events2)
	}
}

func TestTelnetDecoderResetForDecompressed(t *testing.T) {
	d := newTelnetDecoder()
	events := d.Feed([]byte{telIAC, telSB, mccp2, telIAC, telSE, 0x01})
	if len(events) != 1 || events[0].typ != telEventMCCP2Start {
		t.Fatalf("expected MCCP2Start event, got %v", events)
	}
	if !d.IsCompressed() {
		t.Fatal("expected compressed state")
	}

	d.ResetForDecompressed()
	if d.IsCompressed() {
		t.Fatal("expected not compressed after reset")
	}
	if d.RemainingCompressed() != nil {
		t.Fatal("expected nil remaining after reset")
	}

	// After reset, decoder should work normally again
	events2 := d.Feed([]byte("hello"))
	if len(events2) != 1 || events2[0].typ != telEventText || string(events2[0].data) != "hello" {
		t.Fatalf("expected text 'hello' after reset, got %v", events2)
	}
}

func TestTelnetDecoderDuplicateMCCPWhileActive(t *testing.T) {
	d := newTelnetDecoder()

	// 1. Initial MCCP start
	events := d.Feed([]byte{telIAC, telSB, mccp2, telIAC, telSE})
	if len(events) != 1 || events[0].typ != telEventMCCP2Start {
		t.Fatalf("expected MCCP2Start, got %v", events)
	}

	// 2. Runtime activation sequence
	d.SetCompressionActive(true)
	d.ResetForDecompressed()

	// 3. Duplicate MCCP start inside decompressed stream
	// Input: "A" IAC SB MCCP2 IAC SE "B"
	data := []byte{'A', telIAC, telSB, mccp2, telIAC, telSE, 'B'}
	events2 := d.Feed(data)

	// Should NOT emit MCCP2Start again
	if len(events2) != 3 {
		t.Fatalf("expected 3 events (text, SB, text), got %d: %v", len(events2), events2)
	}
	if events2[0].typ != telEventText || string(events2[0].data) != "A" {
		t.Errorf("expected text A, got %v", events2[0])
	}
	if events2[1].typ != telEventSB || events2[1].opt != mccp2 {
		t.Errorf("expected SB mccp2, got %v", events2[1])
	}
	if events2[2].typ != telEventText || string(events2[2].data) != "B" {
		t.Errorf("expected text B, got %v", events2[2])
	}
}
