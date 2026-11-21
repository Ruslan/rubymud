package mapper

import (
	"testing"
	"time"

	"rubymud/go/internal/storage"
)

// A room block that answers a command the player sent while NO move is queued —
// a look, or the server re-printing the room after an action — is first of all
// evidence about the cell the player is already standing in. These tests pin
// that the tracker checks the current cell before it goes looking for a
// neighbour or a global fingerprint match: on a map with repeated identical rooms
// (a monastery's cells and corridors) those searches cannot tell "I looked" from
// "I moved", and skipping the current cell turned every look into a loss or a
// jump.
//
// A block that arrives with NOTHING sent (following, an NPC escort, a server
// push) cannot be a look, so it keeps the searches exactly as before.

// twinCellIndex is a corridor cell with an identical twin far away — the same
// name, description and exits, so the twin shares the fingerprint and a global
// resolve can no longer single the current cell out.
func twinCellIndex() *Index {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Коридор", "пыльный коридор", "E", 0),
		mkRoom("Z", 0, 1, 0, 2, "Тестовая келья", "аскетичная келья", "W", 0),
		mkRoom("Z", 5, 5, 0, 3, "Тестовая келья", "аскетичная келья", "W", 0), // far twin
	}
	return buildTestIndex(1, rooms)
}

// identicalCorridorIndex: two word-for-word identical connected cells, and a
// different room beyond them.
func identicalCorridorIndex() *Index {
	return buildTestIndex(1, []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Коридор", "пыльный коридор", "E W", 0),
		mkRoom("Z", 0, 1, 0, 2, "Коридор", "пыльный коридор", "E W", 0),
		mkRoom("Z", 0, 2, 0, 3, "Коридор", "коридор у лестницы", "W", 0),
	})
}

func TestTrackerLookInPlaceWithDistantTwinKeepsPosition(t *testing.T) {
	tr := NewTracker(twinCellIndex())
	tr.Anchor(Coord{"Z", 0, 1, 0})

	tr.NoteCommand() // "см"
	pos, changed := tr.Reconcile(ev("Тестовая келья", "аскетичная келья", "W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 1, 0}) || !pos.EventDescribesCell {
		t.Fatalf("look in place with a distant twin = %+v, want green on the current cell", pos)
	}
	if changed {
		t.Errorf("a look in place must not report a position change: %+v", pos)
	}
}

func TestTrackerLookInPlaceIgnoresDoorStateLikeEveryOtherMatch(t *testing.T) {
	// Someone shut the door between two looks: the exits line now reads "(W)".
	// Door markers are not identity (mapimport.Fingerprint canonicalizes them).
	tr := NewTracker(twinCellIndex())
	tr.Anchor(Coord{"Z", 0, 1, 0})

	tr.NoteCommand()
	pos, _ := tr.Reconcile(ev("Тестовая келья", "аскетичная келья", "(W)"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("look after a door closed = %+v, want green on the current cell", pos)
	}
}

func TestTrackerLookInPlaceDoesNotJumpToAdjacentTwin(t *testing.T) {
	// Two identical corridor cells side by side, connected. The neighbour search
	// matches the east twin uniquely; without checking the current cell first a
	// look silently green-anchored the player one cell east.
	tr := NewTracker(identicalCorridorIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})

	tr.NoteCommand()
	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 0, 0}) {
		t.Fatalf("look in place beside an identical twin = %+v, want green on the current cell", pos)
	}
}

func TestTrackerCommandlessMoveIntoIdenticalNeighbourStillMoves(t *testing.T) {
	// Nothing was sent: the server moved the player (following, an escort, an
	// automatic flee) into the identical cell next door. That block cannot be a look, so
	// the neighbour search decides exactly as it always did — and the next real
	// step must then start from the right cell, or it lands on a same-named room
	// at 🟡 with the event "describing" it, which «Записать комнату» would
	// overwrite.
	tr := NewTracker(identicalCorridorIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})

	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("commandless move into the identical neighbour = %+v, want green on it", pos)
	}
	tr.PushMove("E")
	pos, _ = tr.Reconcile(ev("Коридор", "коридор у лестницы", "W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 2, 0}) {
		t.Fatalf("next step after the commandless move = %+v, want green on (0,2)", pos)
	}
}

func TestTrackerQueuedStepIntoIdenticalNeighbourMoves(t *testing.T) {
	// A queued step is a move even when the room it lands in reads exactly like
	// the one it left, and even when some other command was sent meanwhile: the
	// FIFO, not the text, says what happened.
	tr := NewTracker(identicalCorridorIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})

	tr.PushMove("E")
	tr.NoteCommand()
	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("queued step into the identical neighbour = %+v, want green on it", pos)
	}
	if tr.PendingCount() != 0 {
		t.Fatalf("the step was not popped: pending=%v", tr.Pending())
	}
}

func TestTrackerLookQueuedBehindAStepIsStillALook(t *testing.T) {
	// "e;см" typed together: the step's block arrives first and must not use up
	// the look, whose block arrives second.
	tr := NewTracker(identicalCorridorIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})

	tr.PushMove("E")
	tr.NoteCommand()
	tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("look answered after the step = %+v, want green on (0,1)", pos)
	}
}

func TestTrackerOneCommandAnswersOneBlock(t *testing.T) {
	// A look is answered by the block it produced. A later block with nothing
	// sent since is a move again.
	tr := NewTracker(identicalCorridorIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})

	tr.NoteCommand()
	tr.Reconcile(ev("Коридор", "пыльный коридор", "E W")) // the look
	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("a block with nothing sent after the look = %+v, want the commandless move east", pos)
	}
}

func TestTrackerLookInPlaceOnUnrecordedCellKeepsAssumedPosition(t *testing.T) {
	// The player stepped onto ground the map does not have yet: 🟡 "edge/cell not
	// in map", which is exactly the state «Записать комнату» writes from. Looking
	// around before recording must not throw that away — a 🔴 refuses the record
	// and forces a walk out and back in.
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 2, 0, 0})
	tr.PushMove("S")
	first, _ := tr.Reconcile(ev("Тестовая кладовая", "уютное пространство", "N"))
	if first.Confidence != Yellow || first.Coord != (Coord{"Z", 3, 0, 0}) || !first.EventDescribesCell {
		t.Fatalf("precondition: step onto unmapped ground = %+v", first)
	}

	tr.NoteCommand()
	pos, changed := tr.Reconcile(ev("Тестовая кладовая", "уютное пространство", "N"))
	if pos.Confidence != Yellow || pos.Coord != first.Coord || !pos.EventDescribesCell {
		t.Fatalf("look on an unrecorded cell = %+v, want the same 🟡 assumed cell (recordable)", pos)
	}
	if pos.Reason != first.Reason || changed {
		t.Errorf("look on an unrecorded cell changed the position: before %+v after %+v", first, pos)
	}
}

func TestTrackerCommandlessMoveBetweenIdenticalUnrecordedRoomsIsLost(t *testing.T) {
	// Following someone from one unrecorded forest room into an identical one:
	// nothing was sent, so this is not "the same block again" but an unexplained
	// move. Holding a recordable 🟡 here would let «Записать комнату» draw the
	// second room on the first one's cell and drift every record after it.
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 2, 0, 0})
	tr.PushMove("S")
	tr.Reconcile(ev("Лес", "густой лес", "N S"))

	pos, _ := tr.Reconcile(ev("Лес", "густой лес", "N S"))
	if pos.Confidence != Red || pos.EventDescribesCell {
		t.Fatalf("commandless move between identical unrecorded rooms = %+v, want 🔴 (not recordable)", pos)
	}
}

func TestTrackerLookAfterStepOntoUnrecordedGroundDoesNotReturnToDeparture(t *testing.T) {
	// Extending a word-for-word repeated corridor: the step east onto unrecorded
	// ground printed the same block as the cell it left. A look then reprints it.
	// The cell the step departed from is the one place the look cannot put us:
	// the server accepted the step. Before, the neighbour search (or, with only
	// the departure cell mapped, the unique fingerprint) took us straight back.
	for name, rooms := range map[string][]storage.Room{
		"departure has a mapped twin": {
			mkRoom("Z", 0, 0, 0, 1, "Коридор", "пыльный коридор", "E W", 0),
			mkRoom("Z", 0, 1, 0, 2, "Коридор", "пыльный коридор", "E W", 0),
		},
		"departure is unique in the map": {
			mkRoom("Z", 0, 1, 0, 2, "Коридор", "пыльный коридор", "E W", 0),
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr := NewTracker(buildTestIndex(1, rooms))
			tr.Anchor(Coord{"Z", 0, 1, 0})
			tr.PushMove("E")
			step, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
			if step.Confidence != Yellow || step.Coord != (Coord{"Z", 0, 2, 0}) {
				t.Fatalf("precondition: step onto unrecorded (0,2) = %+v", step)
			}

			tr.NoteCommand()
			pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
			if pos.Confidence != Yellow || pos.Coord != (Coord{"Z", 0, 2, 0}) || !pos.EventDescribesCell {
				t.Fatalf("look after the step = %+v, want the same recordable 🟡 (0,2)", pos)
			}
		})
	}
}

func TestTrackerLookOnUnrecordedGroundYieldsToMatchingMappedNeighbour(t *testing.T) {
	// A field sequence: "вниз" refused by a shut hatch in
	// words the tracker does not know, so a phantom D stays queued; the next real
	// steps are then dead-reckoned one level too low, onto ground the map does not
	// have. The look that follows must still let the MAPPED cell above win over
	// that guess — it is how the position found its way back in the field, and an
	// unconditional "nothing moved" would have held the wrong level instead (and a
	// record would have written the room there).
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Кладовая", "полки до потолка", "N D", 0),
		mkRoom("Z", -1, 0, 0, 2, "Первый этаж", "просторный холл", "S", 0),
	}
	tr := NewTracker(buildTestIndex(1, rooms))
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("D") // refused by the server, never cancelled
	tr.PushMove("N")
	tr.Reconcile(ev("Первый этаж", "просторный холл", "S")) // consumes the phantom D
	tr.NoteCommand()                                        // "см", answered with the phantom N still queued
	wrong, _ := tr.Reconcile(ev("Первый этаж", "просторный холл", "S"))
	if wrong.Coord != (Coord{"Z", -1, 0, -1}) {
		t.Fatalf("precondition: phantom step should leave the guess a level low, got %+v", wrong)
	}

	tr.NoteCommand() // "см"
	pos, _ := tr.Reconcile(ev("Первый этаж", "просторный холл", "S"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", -1, 0, 0}) {
		t.Fatalf("look on unrecorded ground = %+v, want the matching mapped cell above", pos)
	}
}

func TestTrackerLookClearsAnExitDiffItNoLongerSees(t *testing.T) {
	// A hatch marked mutable was open on arrival (🟢, diff +D). A monk shuts it and
	// the look prints exactly the mapped exits: the diff it reported is gone.
	room := mkRoom("Z", 0, 0, 0, 1, "Склад", "ящики", "N", 0)
	room.MutableExits = `["D"]`
	tr := NewTracker(buildTestIndex(1, []storage.Room{room, mkRoom("Z", -1, 0, 0, 2, "Двор", "двор", "S", 0)}))
	tr.Anchor(Coord{"Z", -1, 0, 0})
	tr.PushMove("S")
	in, _ := tr.Reconcile(ev("Склад", "ящики", "N D"))
	if in.Confidence != Green || len(in.ExitsAddedLive) != 1 {
		t.Fatalf("precondition: arrival with the mutable hatch open = %+v", in)
	}

	tr.NoteCommand()
	pos, _ := tr.Reconcile(ev("Склад", "ящики", "N"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 0, 0}) || len(pos.ExitsAddedLive)+len(pos.ExitsRemovedMap) != 0 || pos.Reason != "" {
		t.Fatalf("look with the mapped exits = %+v, want a clean 🟢 with no exit diff", pos)
	}
}

func TestTrackerDifferentRoomOnUnrecordedCellIsNotALook(t *testing.T) {
	// Control: on unrecorded ground only the SAME room text counts as "nothing
	// moved". A different room is still a move, command or not.
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 2, 0, 0})
	tr.PushMove("S")
	tr.Reconcile(ev("Тестовая кладовая", "уютное пространство", "N"))

	tr.NoteCommand()
	pos, _ := tr.Reconcile(ev("Чулан", "тесно", "E"))
	if pos.EventDescribesCell && pos.Coord == (Coord{"Z", 3, 0, 0}) {
		t.Fatalf("a different room was taken as a look in place: %+v", pos)
	}
}

func TestTrackerLookInPlaceNeverRevivesALostPosition(t *testing.T) {
	// 🔴 keeps the last-known coord only for display; it is not a claim that the
	// player is there. lookInPlace has no 🔴 check of its own: it relies on every
	// loss clearing EventDescribesCell. Reach 🔴 through the real loss paths, then
	// send a look whose block matches the held cell exactly — the only thing that
	// stands between it and a silent re-green is that invariant.
	losses := map[string]func(tr *Tracker){
		"text veto during a step": func(tr *Tracker) {
			tr.PushMove("W") // (0,1) -> (0,0) "Коридор"; the block says otherwise
			tr.Reconcile(ev("Совсем другая комната", "нигде", "N"))
		},
		"true loss with nothing queued": func(tr *Tracker) {
			tr.Reconcile(ev("Незнакомое место", "нигде", "N"))
		},
		"timed-out move": func(tr *Tracker) {
			tr.Invalidate("timed out")
			tr.pos.Valid, tr.pos.Coord = true, Coord{"Z", 0, 1, 0}
		},
	}
	for name, lose := range losses {
		t.Run(name, func(t *testing.T) {
			tr := NewTracker(twinCellIndex())
			tr.Anchor(Coord{"Z", 0, 1, 0})
			lose(tr)
			lost := tr.Position()
			if lost.Confidence != Red || lost.EventDescribesCell {
				t.Fatalf("precondition: a loss must be 🔴 and describe nothing, got %+v", lost)
			}
			if lost.Coord != (Coord{"Z", 0, 1, 0}) {
				t.Fatalf("precondition: the lost coord should be the matching cell, got %+v", lost)
			}

			tr.NoteCommand()
			pos, _ := tr.Reconcile(ev("Тестовая келья", "аскетичная келья", "W"))
			if pos.Confidence == Green || pos.EventDescribesCell {
				t.Fatalf("a lost position was revived by a matching look: %+v", pos)
			}
		})
	}
}

func TestTrackerAnchorForgetsTheLastLook(t *testing.T) {
	// An explicit anchor moves the player by fiat; the text seen at the OLD cell
	// must not later count as "the same block again" at the new one.
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 2, 0, 0})
	tr.PushMove("S")
	tr.Reconcile(ev("Тестовая кладовая", "уютное пространство", "N"))
	tr.Anchor(Coord{"Z", 9, 9, 0}) // unmapped, yellow

	tr.NoteCommand()
	pos, _ := tr.Reconcile(ev("Тестовая кладовая", "уютное пространство", "N"))
	if pos.EventDescribesCell && pos.Coord == (Coord{"Z", 9, 9, 0}) && pos.Confidence != Red {
		t.Fatalf("stale look text was accepted at a newly anchored cell: %+v", pos)
	}
}

// fakeClock lets the reply-window tests move time without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func withFakeClock(tr *Tracker) *fakeClock {
	c := &fakeClock{t: time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)}
	tr.now = c.now
	return c
}

func TestTrackerRefusedStepThenLooksHealsToTheMappedCell(t *testing.T) {
	// At the frontier: "n" refused in words the tracker does not know ("Сначала
	// встаньте!", "Вы сражаетесь"), so a phantom N stays queued and the first look
	// is dead-reckoned onto unrecorded ground north. The second look must heal
	// back onto the mapped hall. The step that "left" the hall was consumed by a
	// look the player sent after it, so it is no evidence the server accepted a
	// move — the hall must not be excluded as the step's departure.
	tr := NewTracker(buildTestIndex(1, []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Зал", "каменный зал", "N", 0),
	}))
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("N") // refused, never cancelled
	tr.NoteCommand() // "см"
	first, _ := tr.Reconcile(ev("Зал", "каменный зал", "N"))
	if first.Coord != (Coord{"Z", -1, 0, 0}) {
		t.Fatalf("precondition: the phantom step should put the guess north, got %+v", first)
	}

	tr.NoteCommand() // "см"
	pos, _ := tr.Reconcile(ev("Зал", "каменный зал", "N"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 0, 0}) {
		t.Fatalf("second look after a refused step = %+v, want 🟢 back on the hall", pos)
	}
}

func TestTrackerCommandBeforeAStepIsAnsweredBeforeIt(t *testing.T) {
	// Chat (or "сч", or "следовать X") then a step: the server answers in order,
	// so the chat's reply came before the step's block and the command is spent.
	// When the group leader then drags the player into the identical cell next
	// door, that block answers nothing — it is a move.
	tr := NewTracker(buildTestIndex(1, []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Холл", "просторный холл", "E", 0),
		mkRoom("Z", 0, 1, 0, 2, "Коридор", "пыльный коридор", "E W", 0),
		mkRoom("Z", 0, 2, 0, 3, "Коридор", "пыльный коридор", "E W", 0),
	}))
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.NoteCommand() // chat
	tr.PushMove("E")
	step, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if step.Confidence != Green || step.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("precondition: the step = %+v", step)
	}

	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W")) // followed east
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 2, 0}) {
		t.Fatalf("follow after chat and a step = %+v, want 🟢 on the identical cell east", pos)
	}
}

func TestTrackerCommandOutsideTheReplyWindowIsNotALook(t *testing.T) {
	// Something was sent, but long before this block: whatever it was has been
	// answered (or never will be). A block arriving now is read the way it would
	// be with nothing sent — as a move.
	tr := NewTracker(identicalCorridorIndex())
	clock := withFakeClock(tr)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.NoteCommand()
	clock.advance(lookReplyWindow + time.Millisecond)

	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 1, 0}) {
		t.Fatalf("block after the reply window = %+v, want the move to the identical cell east", pos)
	}
}

func TestTrackerCommandInsideTheReplyWindowIsALook(t *testing.T) {
	// Control for the window: a laggy but timely reply is still a look.
	tr := NewTracker(identicalCorridorIndex())
	clock := withFakeClock(tr)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.NoteCommand()
	clock.advance(lookReplyWindow)

	pos, _ := tr.Reconcile(ev("Коридор", "пыльный коридор", "E W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 0, 0, 0}) {
		t.Fatalf("look answered within the reply window = %+v, want the current cell", pos)
	}
}
