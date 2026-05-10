package session

const (
	telIAC  = 255
	telDONT = 254
	telDO   = 253
	telWONT = 252
	telWILL = 251
	telSB   = 250
	telGA   = 249
	telEOR  = 239
	telSE   = 240

	mccp2 = 86
)

type telState int

const (
	telStateNormal telState = iota
	telStateIAC
	telStateIACOption
	telStateSB
	telStateSBData
	telStateSBDataIAC
	telStateCompressed
)

type telEventType int

const (
	telEventText telEventType = iota
	telEventFlush
	telEventWill
	telEventWont
	telEventDo
	telEventDont
	telEventMCCP2Start
	telEventSB
)

type telEvent struct {
	typ  telEventType
	opt  byte
	cmd  byte
	data []byte
}

type telnetDecoder struct {
	state               telState
	cmd                 byte
	sbOpt               byte
	mccp2Triggered      bool
	compressionActive   bool
	compressedRemaining []byte
}

func newTelnetDecoder() *telnetDecoder {
	return &telnetDecoder{state: telStateNormal}
}

func (d *telnetDecoder) Feed(data []byte) (events []telEvent) {
	textStart := 0

	for i := 0; i < len(data); i++ {
		b := data[i]

		switch d.state {
		case telStateNormal:
			if b == telIAC {
				if i > textStart {
					events = append(events, telEvent{typ: telEventText, data: append([]byte(nil), data[textStart:i]...)})
				}
				textStart = i + 1
				d.state = telStateIAC
			}

		case telStateIAC:
			textStart = i + 1
			switch b {
			case telIAC:
				events = append(events, telEvent{typ: telEventText, data: []byte{telIAC}})
				textStart = i + 1
				d.state = telStateNormal
			case telWILL, telWONT, telDO, telDONT:
				d.cmd = b
				d.state = telStateIACOption
			case telSB:
				d.state = telStateSB
			case telGA, telEOR:
				events = append(events, telEvent{typ: telEventFlush, cmd: b})
				d.state = telStateNormal
			default:
				d.state = telStateNormal
			}

		case telStateIACOption:
			textStart = i + 1
			d.state = telStateNormal
			switch d.cmd {
			case telWILL:
				events = append(events, telEvent{typ: telEventWill, opt: b})
			case telWONT:
				events = append(events, telEvent{typ: telEventWont, opt: b})
			case telDO:
				events = append(events, telEvent{typ: telEventDo, opt: b})
			case telDONT:
				events = append(events, telEvent{typ: telEventDont, opt: b})
			}

		case telStateSB:
			textStart = i + 1
			d.sbOpt = b
			d.state = telStateSBData

		case telStateSBData:
			textStart = i + 1
			if b == telIAC {
				d.state = telStateSBDataIAC
			}

		case telStateSBDataIAC:
			textStart = i + 1
			if b == telSE {
				if d.sbOpt == mccp2 && !d.compressionActive {
					events = append(events, telEvent{typ: telEventMCCP2Start})
					d.mccp2Triggered = true
					d.compressedRemaining = make([]byte, len(data)-i-1)
					copy(d.compressedRemaining, data[i+1:])
					d.state = telStateCompressed
					return events
				}
				events = append(events, telEvent{typ: telEventSB, opt: d.sbOpt})
				d.state = telStateNormal
			} else if b == telIAC {
				d.state = telStateSBData
			} else {
				d.state = telStateSBData
			}
		}
	}

	if textStart < len(data) {
		events = append(events, telEvent{typ: telEventText, data: append([]byte(nil), data[textStart:]...)})
	}

	return events
}

func (d *telnetDecoder) IsCompressed() bool {
	return d.state == telStateCompressed
}

func (d *telnetDecoder) RemainingCompressed() []byte {
	return d.compressedRemaining
}

func (d *telnetDecoder) ResetForDecompressed() {
	d.state = telStateNormal
	d.mccp2Triggered = false
	d.compressedRemaining = nil
	d.sbOpt = 0
}

func (d *telnetDecoder) SetCompressionActive(active bool) {
	d.compressionActive = active
}
