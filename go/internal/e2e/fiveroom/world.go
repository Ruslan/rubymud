// Package fiveroom implements the deterministic physical world used by the
// mapper reconciliation live E2E. It intentionally has no mapper/storage
// dependency: the emulator is the external truth the host must observe.
package fiveroom

import "strings"

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

type World struct{ current int }

func NewWorld() *World { return &World{current: 1} }

func (w *World) Current() Room { return rooms[w.current] }

func (w *World) Apply(command string) (Room, bool) {
	command = strings.ToUpper(strings.TrimSpace(command))
	next, ok := transitions[w.current][command]
	if !ok {
		return w.Current(), false
	}
	w.current = next
	return w.Current(), true
}

// ScanLines returns the RMUD-shaped surroundings response for the current room.
// It is presentation truth from the fake MUD, not mapper/storage data.
func (w *World) ScanLines() []string {
	prefix := map[string]string{
		"N": "На севере:", "E": "На востоке:", "S": "На юге:",
		"W": "На западе:", "U": "Наверху:", "D": "Внизу:",
	}
	dirs := []string{"N", "E", "S", "W", "U", "D"}
	lines := make([]string, 0, len(transitions[w.current]))
	for _, dir := range dirs {
		if target, ok := transitions[w.current][dir]; ok {
			lines = append(lines, prefix[dir]+" "+rooms[target].Hint+".")
		}
	}
	return lines
}
