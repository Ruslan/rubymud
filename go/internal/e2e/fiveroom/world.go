// Package fiveroom implements the deterministic physical world used by the
// mapper reconciliation live E2E. It intentionally has no mapper/storage
// dependency: the emulator is the external truth the host must observe.
package fiveroom

import (
	"strings"
	"time"
)

const (
	HilloZone        = "Хилло"
	HilloSourceHint  = "Вдоль оврага"
	HilloSourceDesc  = "Ряд высоких холмов, заросший довольно густым лесом, протянулся от моря на востоке до Гор-Стражей на западе. Овраги пересекают его тут и там, а по их дну текут ручейки, впадающие в речку и непосредственно в море. Один из таких оврагов преградил вам путь на восток - поглядев вниз, вы решили отказаться от геройства."
	HilloSourceExits = "N W"
	HilloTargetHint  = "Лесистое предгорье"
	HilloTargetDesc  = "Полоска редколесья разделяет речку и высокие пики Гор-Стражей. Пройти здесь довольно легко, вот только надо внимательно смотреть под ноги - спрятанные в листве камни грозят вам многочисленными шишками и синяками."
	HilloTargetExits = "N S W"
)

type Room struct {
	ID    int
	Hint  string
	Desc  string
	Exits string
}

var rooms = map[int]Room{
	1: {ID: 1, Hint: "Loop Room 1", Desc: "The first chamber has a brass compass rose.", Exits: "E"},
	2: {ID: 2, Hint: "Loop Room 2", Desc: "The second chamber is lined with blue tiles.", Exits: "E W"},
	3: {ID: 3, Hint: "Loop Room 3", Desc: "The third chamber has a narrow southern arch.", Exits: "S W"},
	4: {ID: 4, Hint: "Loop Room 4", Desc: "The fourth chamber begins a long western tunnel.", Exits: "N W"},
	5: {ID: 5, Hint: "Loop Room 5", Desc: "The fifth chamber lies below the first chamber.", Exits: "E N"},
}

var transitions = map[int]map[string]int{
	1: {"E": 2},
	2: {"E": 3, "W": 1},
	3: {"S": 4, "W": 2},
	4: {"N": 3, "W": 5},
	5: {"E": 4, "N": 1},
}

func RoomByID(id int) (Room, bool) {
	room, ok := rooms[id]
	return room, ok
}

type WorldOptions struct {
	// OneWayHillo replays the real Hillo exception from the local map: «Вдоль
	// оврага» leads west into «Лесистое предгорье», which has no reverse east exit.
	OneWayHillo  bool
	DynamicCrate bool
	HiddenHatch  bool
	HatchDelay   time.Duration
}

type World struct {
	current      int
	options      WorldOptions
	crateMoved   bool
	hatchReadyAt time.Time
}

func NewWorld() *World { return &World{current: 1} }

func NewWorldWithOptions(options WorldOptions) *World {
	return &World{current: 1, options: options}
}

func (w *World) Current() Room {
	if w.options.DynamicCrate {
		room := crateRooms[w.current]
		if w.current == 1 && w.crateMoved {
			room.Exits = "N D"
		}
		return room
	}
	if w.options.HiddenHatch {
		return hatchRooms[w.current]
	}
	if w.options.OneWayHillo {
		return hilloRooms[w.current]
	}
	return rooms[w.current]
}

func (w *World) Apply(command string) (Room, bool) {
	command = strings.ToUpper(strings.TrimSpace(command))
	if w.options.HiddenHatch && ((w.current == 2 && command == "D") || (w.current == 3 && command == "U")) {
		if w.hatchReadyAt.IsZero() || time.Now().Before(w.hatchReadyAt) {
			return w.Current(), false
		}
		w.hatchReadyAt = time.Time{}
	}
	next, ok := w.transitions()[w.current][command]
	if !ok {
		return w.Current(), false
	}
	w.current = next
	return w.Current(), true
}

func (w *World) ApplyAction(command string) bool {
	if w.options.DynamicCrate && w.current == 1 && strings.EqualFold(strings.TrimSpace(command), "двигать ящик") {
		w.crateMoved = true
		return true
	}
	if w.options.HiddenHatch {
		trimmed := strings.TrimSpace(command)
		if (w.current == 2 && strings.EqualFold(trimmed, "повернуть рычаг")) ||
			(w.current == 3 && strings.EqualFold(trimmed, "нажать кнопку")) {
			delay := w.options.HatchDelay
			if delay <= 0 {
				delay = 2 * time.Second
			}
			w.hatchReadyAt = time.Now().Add(delay)
			return true
		}
	}
	return false
}

// ScanLines returns the RMUD-shaped surroundings response for the current room.
// It is presentation truth from the fake MUD, not mapper/storage data.
func (w *World) ScanLines() []string {
	if w.options.HiddenHatch {
		return nil // every passage in this fixture is deliberately concealed
	}
	prefix := map[string]string{
		"N": "На севере:", "E": "На востоке:", "S": "На юге:",
		"W": "На западе:", "U": "Наверху:", "D": "Внизу:",
	}
	dirs := []string{"N", "E", "S", "W", "U", "D"}
	transitions := w.transitions()
	lines := make([]string, 0, len(transitions[w.current]))
	for _, dir := range dirs {
		if target, ok := transitions[w.current][dir]; ok {
			if w.options.DynamicCrate && w.current == 1 && dir == "D" {
				lines = append(lines, "Внизу: Черная дыра в полу ведет в темноту.")
				continue
			}
			var room Room
			if w.options.DynamicCrate {
				room = crateRooms[target]
			} else if w.options.OneWayHillo {
				room = hilloRooms[target]
			} else {
				room = rooms[target]
			}
			lines = append(lines, prefix[dir]+" "+room.Hint+".")
		}
	}
	return lines
}

func (w *World) transitions() map[int]map[string]int {
	if w.options.DynamicCrate {
		if w.crateMoved {
			return crateTransitionsRevealed
		}
		return crateTransitionsHidden
	}
	if w.options.HiddenHatch {
		return hatchTransitions
	}
	if w.options.OneWayHillo {
		return hilloTransitions
	}
	return transitions
}

const (
	CrateZone        = "Хилло"
	CrateBoxHint     = "В окружении ящиков"
	CrateBoxDesc     = "Небольшой закуток со всех сторон окружен валом из ящиков, мешков, коробок... И все это покрыто пылью и паутиной. Стоило сюда забираться. Как бы теперь отсюда выбраться, вот вопрос. Ведь все это великолепие свалится на голову - мало не покажется! Да... задачка. Ломая себе надо всем этим голову, вы заметили, что один из ящиков сильно выступает из-под остальных и присели на него отдохнуть."
	CratePassageHint = "Проход между ящиками"
	CratePassageDesc = "Паутина попала вам в лицо, и вы с омерзением сдернули ее. Посветив лампой поближе, вы убедились, что и пауки тут как тут. Да такие жирные! Чем, интересно знать, они могут здесь питаться, не крысами же. Проход, похоже, завел в очередной тупик... Вот разве что слева можно протиснуться немного подальше, только смысла никакого - паутина да пыль, что еще там можно найти!"
)

var crateRooms = map[int]Room{
	1: {ID: 1, Hint: CrateBoxHint, Desc: CrateBoxDesc, Exits: "N"},
	2: {ID: 2, Hint: CratePassageHint, Desc: CratePassageDesc, Exits: "S"},
	3: {ID: 3, Hint: "Под ящиками", Desc: "Черная дыра в полу ведет в темноту.", Exits: "U"},
}

var crateTransitionsHidden = map[int]map[string]int{1: {"N": 2}, 2: {"S": 1}, 3: {"U": 1}}
var crateTransitionsRevealed = map[int]map[string]int{1: {"N": 2, "D": 3}, 2: {"S": 1}, 3: {"U": 1}}

var hilloRooms = map[int]Room{
	1: {ID: 1, Hint: HilloSourceHint, Desc: HilloSourceDesc, Exits: HilloSourceExits},
	2: {ID: 2, Hint: HilloTargetHint, Desc: HilloTargetDesc, Exits: HilloTargetExits},
}

var hilloTransitions = map[int]map[string]int{
	1: {"W": 2},
	2: {},
}

const (
	HatchZone        = "Хилло"
	HatchTurnHint    = "Поворот"
	HatchGazeboHint  = "Беседка"
	HatchDeadEndHint = "Тупик"
)

var hatchRooms = map[int]Room{
	1: {ID: 1, Hint: HatchTurnHint, Desc: "Тропа огибает склон; скрытая дорожка уходит к беседке.", Exits: "N E S"},
	2: {ID: 2, Hint: HatchGazeboHint, Desc: "Старая беседка скрывает люк с рычагом.", Exits: ""},
	3: {ID: 3, Hint: HatchDeadEndHint, Desc: "Подземный тупик; рядом с люком спрятана кнопка.", Exits: ""},
	4: {ID: 4, Hint: "Узкий тоннель", Desc: "Прямой тесный тоннель тянется с севера на юг.", Exits: "N S"},
	5: {ID: 5, Hint: "Конец тоннеля", Desc: "Тоннель заканчивается небольшой площадкой.", Exits: "S"},
}

var hatchTransitions = map[int]map[string]int{
	1: {"W": 2, "N": 4},
	2: {"E": 1, "D": 3},
	3: {"U": 2},
	4: {"S": 1, "N": 5},
	5: {"S": 4},
}
