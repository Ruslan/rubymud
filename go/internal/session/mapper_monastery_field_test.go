package session

import (
	"encoding/json"
	"strings"
	"testing"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

// Regression for a map that repeats rooms word for word, in the RMUD block format.
// The rooms below are a synthetic test location («Тестовая обитель»), shaped like
// the field case that exposed the bug: every cell and every stretch of the upper
// corridor share name, description and exits, and the player keeps typing a look
// ("см") to read the room before recording it.
//
// Every one of those looks was reconciled as if the player might have MOVED:
// with nothing queued the tracker never asked whether the block simply describes
// the cell it already holds. A look then jumped 🟢 into an identical NEIGHBOUR
// (which is how the whole upper floor came to be recorded one column off), went
// 🔴 when an identical cell existed anywhere else on the map, and went 🔴 on a
// just-entered cell that was not recorded yet — the walk-out-and-back-in and the
// manual re-anchors that followed.

const monasteryZone = "Тестовая обитель"

// A cell, seen on entering it and on each look (minimap left column
// differs between visits; the detector strips it).
const fieldMonkCell = `Тестовая келья
......|..............      Тестовая келья: маленькая комната с одной
.....[ ]-............ кроватью и табуретом у стены. На
......|.............. табурете лежит раскрытая тестовая книга,
.....[ ]-............ страницы которой пусты. Узкое окно
......|.............. выходит во двор, и сквозь него видно
....-[ ]-[*]......... кусочек серого неба. В углу стоит
..................... сундук с простым замком, а на полу
..................... лежит плетёный коврик. Больше здесь
..................... ничего нет, и эта комната ничем не
..................... отличается от любой другой кельи
..................... этого тестового этажа, кроме номера на
                      двери, которого никто не замечает.
[ Exits: W ]`

// The same cell after its door was shut: only the door marker differs.
const fieldMonkCellDoorShut = `Тестовая келья
......|..............      Тестовая келья: маленькая комната с одной
.....[ ]-............ кроватью и табуретом у стены. На
......|.............. табурете лежит раскрытая тестовая книга,
.....[ ]-............ страницы которой пусты. Узкое окно
......|.............. выходит во двор, и сквозь него видно
....-[ ]-[*]......... кусочек серого неба. В углу стоит
..................... сундук с простым замком, а на полу
..................... лежит плетёный коврик. Больше здесь
..................... ничего нет, и эта комната ничем не
..................... отличается от любой другой кельи
..................... этого тестового этажа, кроме номера на
                      двери, которого никто не замечает.
[ Exits: (W) ]`

// Another cell on the far side of the floor, recorded earlier in the walk.
const fieldFarMonkCell = `Тестовая келья
.....................      Тестовая келья: маленькая комната с одной
..................... кроватью и табуретом у стены. На
..................... табурете лежит раскрытая тестовая книга,
..................... страницы которой пусты. Узкое окно
..................... выходит во двор, и сквозь него видно
-[v]-[v]-[*]......... кусочек серого неба. В углу стоит
..|...|.............. сундук с простым замком, а на полу
..................... лежит плетёный коврик. Больше здесь
..................... ничего нет, и эта комната ничем не
..................... отличается от любой другой кельи
..................... этого тестового этажа, кроме номера на
                      двери, которого никто не замечает.
[ Exits: W ]`

// The corridor cell outside the near cell.
const fieldCorridorAtCell = `Тестовый коридор
..........|..........      Тестовый коридор тянется вдоль всего этажа:
.........[ ]-........ пол выложен одинаковыми серыми плитами,
..........|.......... а стены выкрашены в один и тот же цвет.
.........[ ]-........ Под потолком висят одинаковые фонари,
..........|.......... и в их свете каждый отрезок коридора
........-[*]-[ ]..... выглядит точно так же, как соседний.
..................... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: N E W ]`

// Two further corridor cells walking west, as recorded (door markers differ,
// identity does not).
const fieldCorridorWest1 = `Тестовый коридор
..............|......      Тестовый коридор тянется вдоль всего этажа:
.............[ ]-.... пол выложен одинаковыми серыми плитами,
..............|...... а стены выкрашены в один и тот же цвет.
.............[ ]-.... Под потолком висят одинаковые фонари,
..........|...|...... и в их свете каждый отрезок коридора
........-[*]-[ ]-[ ]. выглядит точно так же, как соседний.
..................... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: (N) E W ]`

const fieldCorridorWest2 = `Тестовый коридор
..................|..      Тестовый коридор тянется вдоль всего этажа:
.................[ ]- пол выложен одинаковыми серыми плитами,
..................|.. а стены выкрашены в один и тот же цвет.
.................[ ]- Под потолком висят одинаковые фонари,
..........|...|...|.. и в их свете каждый отрезок коридора
........-[*]-[ ]-[ ]- выглядит точно так же, как соседний.
..................... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: N E (W) ]`

// The storeroom, entered from the kitchen before it was recorded.
const fieldCookRoom = `Тестовая кладовая
..........|...|...|..      Тестовая кладовая заставлена полками до
.........[ ]-[ ]-[ ]- самого потолка. На полках стоят ящики
..............|...|.. с одинаковыми надписями, мешки с
.........[ ]-[ ]-[ ]- крупой и связки сушёных трав. У дальней
..........|...|...|.. стены стоит бочка с водой, накрытая
.....[ ].[*].[v].[ ]- деревянной крышкой. Пахнет пылью и
......|...........|.. старым деревом. Пол здесь чуть ниже,
-[ ]-[ ].........[ ]. чем в соседней комнате, и у порога
..|...|...........|.. приходится переступать через доску.
-[ ]-[ ]-[ ]-[ ]-[ ]. Свет падает только из открытой двери,
......|...|.......... поэтому дальние углы почти не видны.
                      В одном из них сложены пустые корзины,
                      в другом стоит метла с обломанной
                      ручкой. Над бочкой висит черпак на
                      верёвке, а рядом прибита полка для
                      кружек. Кружек на ней нет. На полу
                      видны следы, ведущие к полкам и
                      обратно к двери. Кто-то бывает здесь
                      часто, но ничего не трогает. Больше
                      в кладовой ничего интересного нет, и
                      задерживаться тут незачем.
[ Exits: N ]`

// The west end of the upper corridor's north row, as recorded on the way in:
// two cells identical but for door markers.
const fieldRingNorthEast = `Тестовый коридор
.....................      Тестовый коридор тянется вдоль всего этажа:
..................... пол выложен одинаковыми серыми плитами,
..................... а стены выкрашены в один и тот же цвет.
..................... Под потолком висят одинаковые фонари,
..................... и в их свете каждый отрезок коридора
........-[v]-........ выглядит точно так же, как соседний.
..........|.......... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: (E) S W D ]`

const fieldRingNorthWest = `Тестовый коридор
.....................      Тестовый коридор тянется вдоль всего этажа:
..................... пол выложен одинаковыми серыми плитами,
..................... а стены выкрашены в один и тот же цвет.
..................... Под потолком висят одинаковые фонари,
..................... и в их свете каждый отрезок коридора
........-[v]-[v]-.... выглядит точно так же, как соседний.
..........|...|...... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: E (S) W D ]`

// The north-east corridor cell again, on stepping back into it from the cell and
// on the look that followed (identical blocks).
const fieldRingNorthEastAgain = `Тестовый коридор
.....................      Тестовый коридор тянется вдоль всего этажа:
..................... пол выложен одинаковыми серыми плитами,
..................... а стены выкрашены в один и тот же цвет.
..................... Под потолком висят одинаковые фонари,
..................... и в их свете каждый отрезок коридора
-[v]-[v]-[v]-[ ]..... выглядит точно так же, как соседний.
..|...|...|.......... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: E S W D ]`

// South of it: the east side of the ring, not recorded yet.
const fieldRingEastSide = `Тестовый коридор
.....................      Тестовый коридор тянется вдоль всего этажа:
..................... пол выложен одинаковыми серыми плитами,
..................... а стены выкрашены в один и тот же цвет.
-[v]-[v]-[v]-[ ]..... Под потолком висят одинаковые фонари,
..|...|...|.......... и в их свете каждый отрезок коридора
.........[*]-........ выглядит точно так же, как соседний.
..........|.......... По обе стороны видны одинаковые двери
..................... без табличек и номеров. Здесь легко
..................... потерять счёт шагам, потому что вокруг
..................... ничего не меняется от одной клетки
..................... до другой.
[ Exits: N (E) S ]`

const fieldPrompt = "100ж 100б> "

// fieldEvent parses one block with the session's default (RMUD) format.
func fieldEvent(t *testing.T, block string) mapper.RoomEvent {
	t.Helper()
	det := mapper.NewSpecDetector(mapper.BuiltinRMUD(), mapper.RMUDDetectorInfo())
	for _, line := range strings.Split(block, "\n") {
		if ev, ok := det.Feed(mapper.Line{Stripped: line, Raw: line}); ok {
			return ev
		}
	}
	t.Fatalf("fixture block did not parse as a room:\n%s", block)
	return mapper.RoomEvent{}
}

// fieldRecorded is the room «Записать комнату» wrote from that block: the live
// hint, description and exits at the given cell.
func fieldRecorded(t *testing.T, block string, x, y, l int) storage.Room {
	t.Helper()
	ev := fieldEvent(t, block)
	dirs, doors := mapimport.NormExits(ev.Exits)
	if dirs == nil {
		dirs = []string{}
	}
	if doors == nil {
		doors = []string{}
	}
	ej, _ := json.Marshal(dirs)
	dj, _ := json.Marshal(doors)
	return storage.Room{Zone: monasteryZone, X: x, Y: y, L: l,
		Hint: ev.Hint, Desc: ev.Desc, Exits: ev.Exits, EDirs: string(ej), Doors: string(dj)}
}

func fieldSession(t *testing.T, rooms []storage.Room) *Session {
	t.Helper()
	s := &Session{
		clients:     make(map[int]clientSink),
		mapDetector: mapper.NewSpecDetector(mapper.BuiltinRMUD(), mapper.RMUDDetectorInfo()),
	}
	s.mapTracker = mapper.NewTracker(buildTestMapIndex(1, rooms))
	s.conn = &recordingConn{}
	return s
}

func fieldFeed(s *Session, text string) {
	for _, line := range strings.Split(text, "\n") {
		s.observeIncomingLine(line, line)
	}
	s.observeIncomingLine(fieldPrompt, fieldPrompt)
}

// fieldLook sends "см" through the ordinary-command write path, as the player
// did, and feeds the block the server answered with.
func fieldLook(t *testing.T, s *Session, block string) {
	t.Helper()
	if _, err := s.writeCommandPayload([]byte("см\n")); err != nil {
		t.Fatalf("send look: %v", err)
	}
	fieldFeed(s, block)
}

func fieldAt(x, y, l int) mapper.Coord {
	return mapper.Coord{Zone: monasteryZone, X: x, Y: y, L: l}
}

func requireFieldPosition(t *testing.T, s *Session, step string, want mapper.Coord, conf mapper.Confidence) {
	t.Helper()
	pos := s.mapTracker.Position()
	if !pos.Valid || pos.Coord != want || pos.Confidence != conf || !pos.EventDescribesCell {
		t.Fatalf("%s: position = %+v, want %s on %v with the event describing it", step, pos, conf, want)
	}
	if st := s.LiveRecordState(); st.Pending != 0 {
		t.Fatalf("%s: %d moves still pending", step, st.Pending)
	}
}

func TestMonasteryFieldLookInCorridorDoesNotShiftTheRecordedRing(t *testing.T) {
	// The step that drew the upper floor one column off. Back in the corridor
	// from the east cell, the player looked; the look
	// jumped 🟢 onto the identical cell to the west, and the next step south — onto
	// unrecorded ground — was then dead-reckoned (and recorded) one column west of
	// where the player stood. Every cell recorded afterwards inherited the offset
	// until it was moved by hand.
	s := fieldSession(t, []storage.Room{
		fieldRecorded(t, fieldRingNorthWest, 0, 2, 3),
		fieldRecorded(t, fieldRingNorthEast, 0, 3, 3),
		fieldRecorded(t, fieldFarMonkCell, 0, 4, 3),
	})
	s.mapTracker.Anchor(fieldAt(0, 4, 3))

	s.observeOutgoingMove("запад", "key")
	fieldFeed(s, fieldRingNorthEastAgain)
	requireFieldPosition(t, s, "west out of the monk's cell", fieldAt(0, 3, 3), mapper.Green)

	fieldLook(t, s, fieldRingNorthEastAgain)
	requireFieldPosition(t, s, "look in the corridor", fieldAt(0, 3, 3), mapper.Green)

	s.observeOutgoingMove("юг", "key")
	fieldFeed(s, fieldRingEastSide)
	requireFieldPosition(t, s, "south onto the unrecorded east side", fieldAt(1, 3, 3), mapper.Yellow)
}

func TestMonasteryFieldLookInMonkCellWithTwinKeepsPosition(t *testing.T) {
	// The near cell (3,3,3) has just been recorded and
	// anchored; the far cell (0,4,3), recorded two minutes earlier, is its
	// word-for-word twin.
	far := fieldRecorded(t, fieldFarMonkCell, 0, 4, 3)
	near := fieldRecorded(t, fieldMonkCell, 3, 3, 3)
	if mapimport.Fingerprint(far.Hint, far.Desc, far.Exits) != mapimport.Fingerprint(near.Hint, near.Desc, near.Exits) {
		t.Fatal("fixture: the two monk cells must be fingerprint twins, as they were in the field")
	}
	s := fieldSession(t, []storage.Room{
		far,
		fieldRecorded(t, fieldCorridorAtCell, 3, 2, 3),
		near,
	})
	s.mapTracker.Anchor(fieldAt(3, 3, 3))

	fieldLook(t, s, fieldMonkCell)
	requireFieldPosition(t, s, "look in the monk's cell", fieldAt(3, 3, 3), mapper.Green)

	s.observeOutgoingMove("запад", "key")
	fieldFeed(s, fieldCorridorAtCell)
	requireFieldPosition(t, s, "west to the corridor", fieldAt(3, 2, 3), mapper.Green)

	s.observeOutgoingMove("восток", "key")
	fieldFeed(s, fieldMonkCell)
	requireFieldPosition(t, s, "back east into the cell", fieldAt(3, 3, 3), mapper.Green)

	fieldLook(t, s, fieldMonkCellDoorShut) // after a monk shut the door
	requireFieldPosition(t, s, "look after the door was shut", fieldAt(3, 3, 3), mapper.Green)
}

func TestMonasteryFieldLookInCorridorDoesNotJumpToTwinNeighbour(t *testing.T) {
	// Two recorded corridor cells side by side, identical but
	// for door markers. A step west is refused by a shut door, then a look.
	s := fieldSession(t, []storage.Room{
		fieldRecorded(t, fieldCorridorAtCell, 3, 2, 3),
		fieldRecorded(t, fieldCorridorWest1, 3, 1, 3),
		fieldRecorded(t, fieldCorridorWest2, 3, 0, 3),
	})
	s.mapTracker.Anchor(fieldAt(3, 0, 3))

	s.observeOutgoingMove("запад", "key")
	fieldFeed(s, "Дверь закрыта.")
	fieldLook(t, s, fieldCorridorWest2)
	requireFieldPosition(t, s, "look after the refused step", fieldAt(3, 0, 3), mapper.Green)
}

func TestMonasteryFieldLookInUnrecordedCookRoomStaysRecordable(t *testing.T) {
	// South from the recorded kitchen into the storeroom,
	// which the map did not have yet; the player looked before recording. The
	// kitchen's own text plays no part in this step, only its cell and exits.
	kitchen := storage.Room{Zone: monasteryZone, X: 2, Y: 0, L: 2, Hint: "Тестовая кухня",
		Desc: "Тестовая кухня с одним столом.", Exits: "E S", EDirs: `["E","S"]`, Doors: "[]"}
	s := fieldSession(t, []storage.Room{kitchen})
	s.mapTracker.Anchor(fieldAt(2, 0, 2))

	s.observeOutgoingMove("юг", "key")
	fieldFeed(s, fieldCookRoom)
	requireFieldPosition(t, s, "south onto unrecorded ground", fieldAt(3, 0, 2), mapper.Yellow)

	fieldLook(t, s, fieldCookRoom)
	requireFieldPosition(t, s, "look before recording", fieldAt(3, 0, 2), mapper.Yellow)
}

func TestMonasteryFieldBlockWithNothingSentIsStillAMove(t *testing.T) {
	// The same corridor blocks, but nothing was sent: the server moved the player
	// (following the group leader) into the identical
	// cell to the west. That block cannot be a look, so the neighbour search takes
	// the player there exactly as before the look-in-place rule existed.
	s := fieldSession(t, []storage.Room{
		fieldRecorded(t, fieldRingNorthWest, 0, 2, 3),
		fieldRecorded(t, fieldRingNorthEast, 0, 3, 3),
	})
	s.mapTracker.Anchor(fieldAt(0, 3, 3))

	fieldFeed(s, "Вы последовали за Проводником на запад.")
	fieldFeed(s, fieldRingNorthEastAgain)
	requireFieldPosition(t, s, "followed west", fieldAt(0, 2, 3), mapper.Green)
}

func TestMonasteryFieldFailedWriteSendsNoLook(t *testing.T) {
	// A look that never reached the game cannot be what the next block answers.
	s := fieldSession(t, []storage.Room{
		fieldRecorded(t, fieldRingNorthWest, 0, 2, 3),
		fieldRecorded(t, fieldRingNorthEast, 0, 3, 3),
	})
	s.conn = &failingManualConn{}
	s.mapTracker.Anchor(fieldAt(0, 3, 3))

	if _, err := s.writeCommandPayload([]byte("см\n")); err == nil {
		t.Fatal("precondition: the write must fail")
	}
	fieldFeed(s, fieldRingNorthEastAgain)
	requireFieldPosition(t, s, "block after a failed look", fieldAt(0, 2, 3), mapper.Green)
}

func TestMonasteryFieldTypedStepSpendsEarlierChat(t *testing.T) {
	// Through the real command path: the player chats, then types a direction.
	// The chat is an ordinary command; the direction is a queued move, and the
	// server answers in order, so by the time the step's block arrives the chat
	// has been answered. When the group leader then drags the player west into
	// the identical corridor cell, nothing outstanding can explain that block as
	// a look — it is a move.
	store := newTestStoreWithDeclarations(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("VM reload: %v", err)
	}
	s := fieldSession(t, []storage.Room{
		fieldRecorded(t, fieldRingNorthWest, 0, 2, 3),
		fieldRecorded(t, fieldRingNorthEast, 0, 3, 3),
		fieldRecorded(t, fieldFarMonkCell, 0, 4, 3),
	})
	s.sessionID, s.store, s.vm = 1, store, v
	s.mapTracker.Anchor(fieldAt(0, 4, 3))

	if err := s.SendCommand("гг тест", "input"); err != nil {
		t.Fatalf("send chat: %v", err)
	}
	if err := s.SendCommand("запад", "key"); err != nil {
		t.Fatalf("send step: %v", err)
	}
	fieldFeed(s, fieldRingNorthEastAgain)
	requireFieldPosition(t, s, "typed step west", fieldAt(0, 3, 3), mapper.Green)

	fieldFeed(s, "Вы последовали за Проводником на запад.")
	fieldFeed(s, fieldRingNorthEastAgain)
	requireFieldPosition(t, s, "followed west after the step", fieldAt(0, 2, 3), mapper.Green)

	if got := s.conn.(*recordingConn).String(); got != "гг тест\nзапад\n" {
		t.Fatalf("wire = %q, want the chat then the step", got)
	}
}
