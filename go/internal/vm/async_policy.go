package vm

// ProcessInputDetailedNoAsync runs the ordinary parser, substitutions, aliases,
// variables, conditionals and local synchronous effects, but records timer/TTS
// effects instead of starting them. Map-walk uses this for preflight: an async
// callback must not escape after the route has been cancelled or replaced.
func (v *VM) ProcessInputDetailedNoAsync(input string) ([]Result, []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ensureFresh()

	guard := &asyncEffectGuard{readTimer: v.timerCtrl}
	timerCtrl, ttsFn, ttsCustom := v.timerCtrl, v.ttsFn, v.ttsCustom
	v.timerCtrl = guard
	v.ttsFn = func(string) { guard.record("tts") }
	v.ttsCustom = true
	defer func() {
		v.timerCtrl = timerCtrl
		v.ttsFn = ttsFn
		v.ttsCustom = ttsCustom
	}()

	return v.evalLine(input, 0, nil), append([]string(nil), guard.effects...)
}

// HasMultipleStatements exposes the VM's own brace/quote-aware statement split
// without creating a second script parser in route code.
func HasMultipleStatements(input string) bool {
	return len(splitStatements(input)) > 1
}

type asyncEffectGuard struct {
	readTimer TimerControl
	effects   []string
}

func (g *asyncEffectGuard) record(name string) { g.effects = append(g.effects, name) }

func (g *asyncEffectGuard) TickOn(string)                      { g.record("tickon") }
func (g *asyncEffectGuard) TickOff(string)                     { g.record("tickoff") }
func (g *asyncEffectGuard) TickReset(string)                   { g.record("tickset") }
func (g *asyncEffectGuard) TickSet(string, float64)            { g.record("tickset") }
func (g *asyncEffectGuard) TickSize(string, float64)           { g.record("ticksize") }
func (g *asyncEffectGuard) TickIcon(string, string)            { g.record("tickicon") }
func (g *asyncEffectGuard) TickAdjust(string, float64)         { g.record("tickset") }
func (g *asyncEffectGuard) TickMode(string, string)            { g.record("tickmode") }
func (g *asyncEffectGuard) SubscribeTimer(string, int, string) { g.record("tickat") }
func (g *asyncEffectGuard) UnsubscribeTimer(string, int)       { g.record("untickat") }
func (g *asyncEffectGuard) UnsubscribeTimerExact(string, int, string) {
	g.record("untickat")
}
func (g *asyncEffectGuard) ScheduleDelay(string, float64, string) error {
	g.record("delay")
	return nil
}
func (g *asyncEffectGuard) CancelDelay(string) { g.record("undelay") }
func (g *asyncEffectGuard) GetTimerCycleSeconds(name string) int {
	if g.readTimer != nil {
		return g.readTimer.GetTimerCycleSeconds(name)
	}
	return 60
}
