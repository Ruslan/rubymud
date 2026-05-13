package session

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

type clientSink struct {
	id   int
	name string
	send func(msg ServerMsg) error
}

type delayTask struct {
	id      string
	due     time.Time
	command string
}

type cmdItem struct {
	line   string
	source string
}

type Session struct {
	sessionID    int64
	mudAddr      string
	conn         net.Conn
	store        *storage.Store
	vm           *vm.VM
	clients      map[int]clientSink
	nextClientID int
	mu           sync.Mutex
	closed       bool
	timers       map[string]*Timer
	timersMu     sync.Mutex
	delays       map[string]*delayTask
	delaysMu     sync.Mutex
	cmdQueue     chan cmdItem
	done         chan struct{}

	outputBatch      []ClientLogEntry
	outputBatchHints []ServerMsg
	batchActive      bool
	batchOldestLine  time.Time
	batchLatency     latencyAggregate

	readSrc      io.Reader
	initCmds     string
	mccpOn       bool
	mccpAccepted bool
	zlibCloser   io.Closer

	mccpActive            atomic.Bool
	mccpCompressedBytes   atomic.Uint64
	mccpDecompressedBytes atomic.Uint64

	lastCommandAt time.Time
}

func New(sessionID int64, mudAddr string, store *storage.Store, v *vm.VM, initCmds string, mccpOn bool) (*Session, error) {
	conn, err := net.Dial("tcp", mudAddr)
	if err != nil {
		return nil, err
	}

	s := &Session{
		sessionID: sessionID,
		mudAddr:   mudAddr,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan cmdItem, 100),
		done:      make(chan struct{}),
		readSrc:   conn,
		initCmds:  initCmds,
		mccpOn:    mccpOn,
	}

	s.restoreTimers()

	v.SetTimerControl(s)

	go s.runTimerLoop()
	go s.runCommandDispatcher()

	return s, nil
}

func (s *Session) runCommandDispatcher() {
	for {
		select {
		case item := <-s.cmdQueue:
			if item.source == "" {
				item.source = "scheduler"
			}
			if err := s.SendCommand(item.line, item.source); err != nil {
				log.Printf("scheduler send command error: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		case <-s.done:
			return
		}
	}
}

func (s *Session) runTimerLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastRemSecs := make(map[string]int)

	for {
		select {
		case <-ticker.C:
			s.timersMu.Lock()
			changed := false
			var commandsToRun []string

			for name, t := range s.timers {
				rem, cmds := t.CheckSubscriptions()
				if last, ok := lastRemSecs[name]; ok && last != rem && rem >= 0 {
					// Second boundary crossed
					if len(cmds) > 0 {
						commandsToRun = append(commandsToRun, cmds...)
					}
				}
				lastRemSecs[name] = rem

				if t.Check() {
					changed = true
				}
			}
			if changed {
				s.BroadcastTick()
			}
			s.timersMu.Unlock()

			// Handle delays
			now := time.Now()
			s.delaysMu.Lock()
			for id, task := range s.delays {
				if now.After(task.due) {
					commandsToRun = append(commandsToRun, task.command)
					delete(s.delays, id)
				}
			}
			s.delaysMu.Unlock()

			// Queue commands
			for _, cmd := range commandsToRun {
				select {
				case s.cmdQueue <- cmdItem{line: cmd, source: "scheduler"}:
				default:
					log.Printf("command queue full, dropping scheduled command: %s", cmd)
				}
			}
		case <-s.done:
			return
		}
	}
}

func (s *Session) BroadcastTick() {
	// Caller must hold s.timersMu
	s.mu.Lock()
	if len(s.clients) == 0 {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	var snapshots []TimerSnapshot
	for name, t := range s.timers {
		if name == "ticker" || t.Enabled {
			snapshots = append(snapshots, t.Snapshot())
		}
	}

	s.broadcastMsg(ServerMsg{
		Type:   "tick",
		Timers: snapshots,
	})
}

// TimerControl implementation
func (s *Session) TickOn(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	if t.On() {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) ensureTimer(name string) *Timer {
	t, ok := s.timers[name]
	if ok {
		return t
	}

	// Try to load from declaration (works for both named and 'ticker')
	if t := s.loadProfileTimerDeclaration(name); t != nil {
		s.timers[name] = t
		s.persistTimer(t)
		s.persistSubscriptions(t)
		return t
	}

	// Fallback to default auto-create
	t = NewTimer(name, 60*time.Second)
	s.timers[name] = t
	return t
}

func (s *Session) loadProfileTimerDeclaration(name string) *Timer {
	if s.store == nil {
		return nil
	}
	profileID, err := s.store.GetPrimaryProfileID(s.sessionID)
	if err != nil {
		return nil
	}
	decls, err := s.store.GetProfileTimers(profileID)
	if err != nil {
		return nil
	}
	for _, d := range decls {
		if d.Name == name {
			t := NewTimer(name, time.Duration(d.CycleMS)*time.Millisecond)
			t.Icon = d.Icon
			t.RepeatMode = d.RepeatMode
			if t.RepeatMode == "" {
				t.RepeatMode = "repeating"
			}

			// Also load subscriptions
			subs, err := s.store.GetProfileTimerSubscriptions(profileID, name)
			if err == nil {
				for _, sub := range subs {
					t.Subscriptions[sub.Second] = append(t.Subscriptions[sub.Second], sub.Command)
				}
			}
			return t
		}
	}
	return nil
}

func (s *Session) TickOff(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		if t.Off() {
			s.BroadcastTick()
			s.persistTimer(t)
		}
	}
}

func (s *Session) TickReset(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	if t.Reset() {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) TickSet(name string, seconds float64) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	if t.Set(time.Duration(seconds * float64(time.Second))) {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) TickSize(name string, seconds float64) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	if t.Size(time.Duration(seconds * float64(time.Second))) {
		s.BroadcastTick()
		s.persistTimer(t)
		s.persistProfileTimerDeclaration(t)
	}
}

func (s *Session) TickIcon(name string, icon string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	t.SetIcon(icon)
	s.BroadcastTick()
	s.persistTimer(t)
	s.persistProfileTimerDeclaration(t)
}

func (s *Session) TickMode(name string, mode string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	t.mu.Lock()
	t.RepeatMode = mode
	t.mu.Unlock()
	s.BroadcastTick()
	s.persistTimer(t)
	s.persistProfileTimerDeclaration(t)
}

func (s *Session) persistProfileTimerDeclaration(t *Timer) {
	if s.store == nil {
		return
	}
	profileID, err := s.store.GetPrimaryProfileID(s.sessionID)
	if err != nil {
		return
	}
	timerDecl := storage.ProfileTimer{
		ProfileID:  profileID,
		Name:       t.Name,
		CycleMS:    t.CycleMS,
		Icon:       t.Icon,
		RepeatMode: t.RepeatMode,
	}
	if timerDecl.RepeatMode == "" {
		timerDecl.RepeatMode = "repeating"
	}
	if err := s.store.SaveProfileTimer(timerDecl); err != nil {
		log.Printf("failed to persist profile timer declaration %q: %v", t.Name, err)
	}
}

func (s *Session) SubscribeTimer(name string, second int, command string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)

	t.mu.Lock()
	// Deduplicate: check if command already exists for this second
	exists := false
	for _, existing := range t.Subscriptions[second] {
		if existing == command {
			exists = true
			break
		}
	}
	if !exists {
		t.Subscriptions[second] = append(t.Subscriptions[second], command)
	}
	t.mu.Unlock()

	s.persistSubscriptions(t)
	s.persistTimer(t)

	s.persistProfileTimerDeclaration(t)
	s.persistProfileTimerSubscriptions(t)
}

func (s *Session) persistProfileTimerSubscriptions(t *Timer) {
	if s.store == nil {
		return
	}
	profileID, err := s.store.GetPrimaryProfileID(s.sessionID)
	if err != nil {
		return
	}

	t.mu.Lock()
	subsCopy := make(map[int][]string)
	for k, v := range t.Subscriptions {
		subsCopy[k] = append([]string{}, v...)
	}
	t.mu.Unlock()

	s.store.Transaction(func(store *storage.Store) error {
		if err := store.ClearProfileTimerSubscriptions(profileID, t.Name); err != nil {
			return err
		}
		for second, commands := range subsCopy {
			for i, cmd := range commands {
				sub := storage.ProfileTimerSubscription{
					ProfileID: profileID,
					TimerName: t.Name,
					Second:    second,
					SortOrder: i,
					Command:   cmd,
				}
				if err := store.SaveProfileTimerSubscription(sub); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Session) UnsubscribeTimer(name string, second int) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		t.mu.Lock()
		delete(t.Subscriptions, second)
		t.mu.Unlock()

		s.persistSubscriptions(t)
		s.persistTimer(t)

		s.persistProfileTimerSubscriptions(t)
	}
}

func (s *Session) ScheduleDelay(id string, seconds float64, command string) error {
	s.delaysMu.Lock()
	defer s.delaysMu.Unlock()

	if len(s.delays) >= 50 {
		return fmt.Errorf("too many pending delays (max 50)")
	}

	// If id is empty, use a generated one
	if id == "" {
		id = "auto_" + time.Now().Format("150405.000000")
	}

	// Guardrail: minimum 100ms effective delay
	delay := time.Duration(seconds * float64(time.Second))
	if delay < 100*time.Millisecond {
		delay = 100 * time.Millisecond
	}

	s.delays[id] = &delayTask{
		id:      id,
		due:     time.Now().Add(delay),
		command: command,
	}
	return nil
}

func (s *Session) CancelDelay(id string) {
	s.delaysMu.Lock()
	defer s.delaysMu.Unlock()
	delete(s.delays, id)
}

func (s *Session) GetTimerCycleSeconds(name string) int {
	s.timersMu.Lock()
	t, ok := s.timers[name]
	s.timersMu.Unlock()

	if ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		cycle := int(t.Cycle.Seconds())
		if cycle == 0 && name == "ticker" {
			return 60 // Default cycle for default ticker
		}
		return cycle
	}

	// Not in runtime, check declaration
	if name != "ticker" && s.store != nil {
		profileID, err := s.store.GetPrimaryProfileID(s.sessionID)
		if err == nil {
			d, err := s.store.GetProfileTimer(profileID, name)
			if err == nil {
				return d.CycleMS / 1000
			}
		}
	}

	return 60 // Default cycle for any unknown named timer
}

func (s *Session) TickAdjust(name string, deltaSeconds float64) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t := s.ensureTimer(name)
	if t.Adjust(deltaSeconds) {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) persistTimer(t *Timer) {
	if s.store == nil {
		return
	}

	snapshot := t.Snapshot()
	record := storage.TimerRecord{
		SessionID:   s.sessionID,
		Name:        snapshot.Name,
		CycleMS:     snapshot.CycleMS,
		RemainingMS: snapshot.RemainingMS,
		Enabled:     snapshot.Enabled,
		Icon:        snapshot.Icon,
		RepeatMode:  snapshot.RepeatMode,
	}

	if snapshot.Enabled && snapshot.CycleMS > 0 {
		at := storage.SQLiteTime{Time: snapshot.NextTickAt}
		record.NextTickAt = &at
	}

	if err := s.store.SaveTimer(record); err != nil {
		log.Printf("failed to persist timer %q: %v", t.Name, err)
	}
}

func (s *Session) persistSubscriptions(t *Timer) {
	if s.store == nil {
		return
	}

	t.mu.Lock()
	// Create a copy of subscriptions to avoid holding lock during DB operations
	subsCopy := make(map[int][]string)
	for k, v := range t.Subscriptions {
		subsCopy[k] = append([]string{}, v...)
	}
	t.mu.Unlock()

	err := s.store.Transaction(func(store *storage.Store) error {
		if err := store.ClearAllSubscriptions(s.sessionID, t.Name); err != nil {
			return err
		}

		for second, commands := range subsCopy {
			for i, cmd := range commands {
				subRecord := storage.TimerSubscriptionRecord{
					SessionID: s.sessionID,
					TimerName: t.Name,
					Second:    second,
					SortOrder: i,
					Command:   cmd,
				}
				if err := store.SaveSubscription(subRecord); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("failed to persist subscriptions for timer %q: %v", t.Name, err)
	}
}

func (s *Session) restoreTimers() {
	if s.store != nil {
		timers, err := s.store.GetTimers(s.sessionID)
		if err != nil {
			log.Printf("failed to load persisted timers: %v", err)
		} else {
			subs, err := s.store.GetSubscriptions(s.sessionID)
			if err != nil {
				log.Printf("failed to load timer subscriptions: %v", err)
			}

			s.timersMu.Lock()
			for _, rt := range timers {
				cycleMS := rt.CycleMS
				enabled := rt.Enabled

				if cycleMS <= 0 {
					if rt.Name == "ticker" {
						cycleMS = 60000 // Fallback to 60s for default ticker
					} else {
						enabled = false // Disable broken named timers
						cycleMS = 60000 // Keep a safe positive cycle even if disabled
					}
				}

				if enabled && rt.NextTickAt == nil {
					log.Printf("timer %q in session %d had enabled=true but next_tick_at=NULL, forcing disabled", rt.Name, s.sessionID)
					enabled = false
				}

				t := NewTimer(rt.Name, time.Duration(cycleMS)*time.Millisecond)
				t.Enabled = enabled
				t.Icon = rt.Icon
				t.RemainingMS = rt.RemainingMS
				t.RepeatMode = rt.RepeatMode
				if t.RepeatMode == "" {
					t.RepeatMode = "repeating"
				}

				if enabled && rt.NextTickAt != nil {
					nextAt := rt.NextTickAt.Time
					now := time.Now()

					if now.After(nextAt) {
						// Timer was running while server was down
						if rt.RepeatMode == "one_shot" {
							t.Enabled = false
							t.RemainingMS = 0
						} else {
							elapsed := now.Sub(nextAt)
							cycle := time.Duration(cycleMS) * time.Millisecond
							if cycle > 0 {
								// Recalculate phase: remaining = cycle - (elapsed % cycle)
								rem := cycle - (elapsed % cycle)
								t.NextTickAt = now.Add(rem)
								t.RemainingMS = int(rem.Milliseconds())
							} else {
								t.Enabled = false
								t.RemainingMS = 0
							}
						}
					} else {
						t.NextTickAt = nextAt
						t.RemainingMS = int(time.Until(nextAt).Milliseconds())
					}
				}

				// Attach subscriptions
				for _, rs := range subs {
					if rs.TimerName == rt.Name {
						t.Subscriptions[rs.Second] = append(t.Subscriptions[rs.Second], rs.Command)
					}
				}

				s.timers[rt.Name] = t
			}
			s.timersMu.Unlock()
		}
	}

	// Ensure default ticker exists
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if _, ok := s.timers["ticker"]; !ok {
		// No session runtime state for ticker, try profile declaration
		if t := s.loadProfileTimerDeclaration("ticker"); t != nil {
			s.timers["ticker"] = t
			s.persistTimer(t)
			s.persistSubscriptions(t)
		} else {
			// Absolute fallback
			s.timers["ticker"] = NewTimer("ticker", 60*time.Second)
		}
	}
}

func (s *Session) SessionID() int64 {
	return s.sessionID
}

func (s *Session) Variables() map[string]string {
	return s.vm.Variables()
}

func (s *Session) TimerSnapshots() []TimerSnapshot {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	var snapshots []TimerSnapshot
	for name, t := range s.timers {
		if name == "ticker" || t.Enabled {
			snapshots = append(snapshots, t.Snapshot())
		}
	}
	return snapshots
}

func (s *Session) CurrentVariables() ([]VariableJSON, error) {
	vars, err := s.store.ListVariables(s.sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]VariableJSON, 0, len(vars))
	for _, v := range vars {
		result = append(result, VariableJSON{Key: v.Key, Value: v.Value})
	}
	return result, nil
}

func (s *Session) SetVariable(key, value string) error {
	return s.store.SetVariable(s.sessionID, key, value)
}

func (s *Session) DeleteVariable(key string) error {
	return s.store.DeleteVariable(s.sessionID, key)
}

func (s *Session) RecentLogs(limit int) ([]storage.LogEntry, error) {
	return s.store.RecentLogs(s.sessionID, limit)
}

func (s *Session) RecentLogsPerBuffer(limit int) (map[string][]storage.LogEntry, error) {
	return s.store.RecentLogsPerBuffer(s.sessionID, limit)
}

func (s *Session) RecentInputHistory(limit int) ([]string, error) {
	return s.store.RecentInputHistory(s.sessionID, limit)
}

func (s *Session) HighlightText(text string) string {
	if s == nil || s.vm == nil {
		return text
	}
	return s.vm.ApplyHighlights(text)
}

func (s *Session) RenderLogEntry(entry storage.LogEntry) string {
	return s.HighlightText(entry.DisplayRawText())
}

func (s *Session) BroadcastResult(res vm.Result) {
	if res.Kind != vm.ResultEcho || res.IsInternal {
		return
	}

	target := res.TargetBuffer
	if target == "" {
		target = "main"
	}

	// Echo to database
	id, err := s.store.AppendLogEntry(s.sessionID, target, res.Text, res.Text)
	if err != nil {
		log.Printf("failed to append echo to logs: %v", err)
		return
	}

	// Apply highlights and broadcast
	highlighted := s.HighlightText(res.Text)
	s.broadcastEntryWithText(storage.LogEntry{
		ID:        id,
		Buffer:    target,
		RawText:   res.Text,
		PlainText: res.Text,
	}, highlighted)
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.done != nil {
		close(s.done)
	}
	clients := make([]clientSink, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	s.clients = map[int]clientSink{}
	s.mu.Unlock()

	statusMsg := ServerMsg{Type: "status", Status: "disconnected"}
	for _, client := range clients {
		if err := client.send(statusMsg); err != nil {
			log.Printf("client send error to %s: %v", client.name, err)
		}
	}

	log.Printf("closing session for %s", s.mudAddr)
	if s.store != nil {
		if err := s.store.MarkSessionDisconnected(s.sessionID); err != nil {
			log.Printf("mark session disconnected failed: %v", err)
		}
	}
	if s.zlibCloser != nil {
		_ = s.zlibCloser.Close()
	}
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *Session) NotifySettingsChanged(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := ServerMsg{
		Type: "settings.changed",
		Settings: &SettingsChangedJSON{
			Domain: domain,
		},
	}

	for _, client := range s.clients {
		if err := client.send(msg); err != nil {
			log.Printf("failed to send settings notification to client %d: %v", client.id, err)
		}
	}

	// Also reload VM state and rebuild compiled caches
	if err := s.vm.ReloadFromStore(); err != nil {
		log.Printf("failed to reload vm after settings change: %v", err)
	}
}

func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Session) writeTelnet(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if _, err := s.conn.Write(data); err != nil {
		log.Printf("telnet write error: %v", err)
	}
}

func (s *Session) handleTelnetEvent(ev telEvent) {
	switch ev.typ {
	case telEventWill:
		if ev.opt == mccp2 && s.mccpOn {
			log.Printf("telnet: server offers MCCP2, accepting")
			s.writeTelnet([]byte{telIAC, telDO, mccp2})
			s.mccpAccepted = true
		} else {
			s.writeTelnet([]byte{telIAC, telDONT, ev.opt})
		}
	case telEventWont:
	case telEventDo:
		s.writeTelnet([]byte{telIAC, telWONT, ev.opt})
	case telEventDont:
	case telEventSB:
	}
}

func (s *Session) activateMCCP2(decoder *telnetDecoder) {
	remaining := decoder.RemainingCompressed()
	var zlibReader io.ReadCloser
	var err error

	var rawSrc io.Reader = s.conn
	if len(remaining) > 0 {
		rawSrc = io.MultiReader(bytes.NewReader(remaining), s.conn)
	}

	countingSrc := &countingReader{r: rawSrc, count: &s.mccpCompressedBytes}
	zlibReader, err = zlib.NewReader(countingSrc)
	if err != nil {
		log.Printf("MCCP2 activation failed: %v", err)
		s.Close()
		return
	}

	decoder.SetCompressionActive(true)
	s.readSrc = zlibReader
	s.zlibCloser = zlibReader
	s.mccpActive.Store(true)
	log.Printf("MCCP2 compression activated (%d bytes leftover)", len(remaining))
}

func (s *Session) MCCPStats() (bool, uint64, uint64, string) {
	active := s.mccpActive.Load()
	comp := s.mccpCompressedBytes.Load()
	decomp := s.mccpDecompressedBytes.Load()
	ratio := "0%"
	if comp > 0 && decomp > comp {
		pct := 100.0 * (1.0 - float64(comp)/float64(decomp))
		ratio = fmt.Sprintf("%.1f%%", pct)
	}
	return active, comp, decomp, ratio
}

type countingReader struct {
	r     io.Reader
	count *atomic.Uint64
}

func (c *countingReader) Read(p []byte) (n int, err error) {
	n, err = c.r.Read(p)
	c.count.Add(uint64(n))
	return n, err
}

func (s *Session) QueueStartupCommands() {
	if s.initCmds == "" {
		return
	}
	for _, line := range strings.Split(s.initCmds, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		select {
		case s.cmdQueue <- cmdItem{line: line, source: "connect"}:
		default:
			log.Printf("startup command queue full, dropping: %s", line)
		}
	}
}
