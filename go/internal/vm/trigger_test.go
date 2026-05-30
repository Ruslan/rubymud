package vm

import (
	"reflect"
	"testing"

	"rubymud/go/internal/storage"
)

func TestMatchTriggers_BufferRouting(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{
			Pattern:      ` мертв! R\.I\.P\.$`,
			Command:      "",
			TargetBuffer: "kills",
			BufferAction: "copy",
			Enabled:      true,
		},
		{
			Pattern:      `^Вы получили (\d+) очков опыта\.`,
			Command:      "",
			TargetBuffer: "kills",
			BufferAction: "copy",
			Enabled:      true,
		},
	}

	// 1. Check death message
	_, routing1 := v.MatchTriggers("Советник мертв! R.I.P.")
	if len(routing1.CopyBuffers) != 1 || routing1.CopyBuffers[0] != "kills" {
		t.Errorf("expected routing to 'kills' buffer via copy, got %+v", routing1)
	}

	// 2. Check exp message
	_, routing2 := v.MatchTriggers("Вы получили 3639 очков опыта.")
	if len(routing2.CopyBuffers) != 1 || routing2.CopyBuffers[0] != "kills" {
		t.Errorf("expected routing exp to 'kills' buffer, got %+v", routing2)
	}
}

func TestArcticTriggerAnchoredCaret(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You are thirsty\.`, Command: "drink all", Enabled: true},
	}

	effects, _ := v.MatchTriggers("You are thirsty.")
	if len(effects) != 1 {
		t.Fatalf("anchored trigger match = %d, want 1", len(effects))
	}

	effects, _ = v.MatchTriggers("Someone says: You are thirsty.")
	if len(effects) != 0 {
		t.Errorf("anchored trigger should NOT match mid-line, got %d matches", len(effects))
	}
}

func TestArcticTriggerWithCapture(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) is dead!`, Command: "get coins corpse", Enabled: true},
	}

	effects, _ := v.MatchTriggers("The Dragon is dead!")
	if len(effects) != 1 || effects[0].Command != "get coins corpse" {
		t.Errorf("is dead trigger = %v, want send{get coins corpse}", effects)
	}
}

func TestArcticTriggerSplitCoins(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^There were (\d+) coins\.`, Command: "split %1", Enabled: true},
	}

	effects, _ := v.MatchTriggers("There were 42 coins.")
	if len(effects) != 1 || effects[0].Command != "split %1" {
		t.Errorf("split coins trigger = %v, want send{split %%1}", effects)
	}
}

func TestArcticTriggerTwoCaptures(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) swings madly at you with (.+), knocking you to the ground\.`, Command: "stand", Enabled: true},
	}

	effects, _ := v.MatchTriggers("Гоблин swings madly at you with дубина, knocking you to the ground.")
	if len(effects) != 1 || effects[0].Command != "stand" {
		t.Errorf("two-capture trigger = %v, want send{stand}", effects)
	}
}

func TestArcticTriggerFlyLoss(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You feel much heavier\.`, Command: "fly;fly", Enabled: true},
	}

	effects, _ := v.MatchTriggers("You feel much heavier.")
	if len(effects) != 1 {
		t.Fatalf("fly loss trigger = %d effects, want 1", len(effects))
	}

	commands := v.ProcessInput(effects[0].Command)
	if len(commands) != 2 || commands[0] != "fly" || commands[1] != "fly" {
		t.Errorf("fly;fly expansion = %v, want [fly, fly]", commands)
	}
}

func TestArcticTriggerSummonWithMulticmd(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) has summoned you!`, Command: "wake;stand;fly", Enabled: true},
	}

	effects, _ := v.MatchTriggers("Маг has summoned you!")
	if len(effects) != 1 {
		t.Fatalf("summon trigger = %d effects, want 1", len(effects))
	}

	commands := v.ProcessInput(effects[0].Command)
	expected := []string{"wake", "stand", "fly"}
	if len(commands) != len(expected) {
		t.Fatalf("summon expansion = %v, want %v", commands, expected)
	}
	for i := range expected {
		if commands[i] != expected[i] {
			t.Errorf("summon[%d] = %q, want %q", i, commands[i], expected[i])
		}
	}
}

func TestArcticRipButtonTrigger(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `R\.I\.P\.$`, Command: "взя все *.тру", IsButton: true, Enabled: true},
	}

	effects, _ := v.MatchTriggers("Крыса R.I.P.")
	if len(effects) != 1 || effects[0].Type != "button" {
		t.Fatalf("RIP button trigger = %v, want button", effects)
	}
	if effects[0].Command != "взя все *.тру" {
		t.Errorf("button command = %q, want %q", effects[0].Command, "взя все *.тру")
	}
}

func TestMultipleTriggersMatch(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You are hungry\.`, Command: "eat all", Enabled: true},
		{Pattern: `^You are`, Command: "look", Enabled: true},
	}

	effects, _ := v.MatchTriggers("You are hungry.")
	if len(effects) != 2 {
		t.Errorf("two triggers matching same line = %d effects, want 2", len(effects))
	}
}

func TestTriggerNoMatch(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You are thirsty\.`, Command: "drink all", Enabled: true},
	}

	effects, _ := v.MatchTriggers("You are hungry.")
	if len(effects) != 0 {
		t.Errorf("non-matching trigger = %d effects, want 0", len(effects))
	}
}

func TestApplyEffects_FullPipeline(t *testing.T) {
	v := New(nil, 1)
	v.aliases = []storage.AliasRule{
		{Name: "fly", Template: "cast 'fly'", Enabled: true},
	}
	v.variables["t1"] = "орк"

	// Trigger with: alias, variable, and local command
	effects := []Effect{
		{Type: "send", Command: "fly; #showme {set $t1}"},
	}

	var sentCommands []string
	var echoes []Result

	sendFn := func(cmd string, entryID int64, buffer string) error {
		sentCommands = append(sentCommands, cmd)
		return nil
	}
	echoFn := func(res Result) {
		echoes = append(echoes, res)
	}

	v.ApplyEffects(effects, 123, "main", sendFn, echoFn)

	// 1. Should have expanded alias 'fly' -> "cast 'fly'"
	if len(sentCommands) != 1 || sentCommands[0] != "cast 'fly'" {
		t.Errorf("expected sent command 'cast 'fly'', got %v", sentCommands)
	}

	// 2. Should have evaluated local command #showme and substituted variable
	if len(echoes) != 1 || echoes[0].Text != "set орк" {
		t.Errorf("expected echo 'set орк', got %v", echoes)
	}
}

func TestApplyEffectsExecResultIsLocalOnly(t *testing.T) {
	v := New(nil, 1)
	effects := []Effect{{Type: "send", Command: "#exec {./items_db_client} {red;blue}"}}

	var sentCommands []string
	var localResults []Result
	v.ApplyEffects(effects, 123, "main", func(cmd string, _ int64, _ string) error {
		sentCommands = append(sentCommands, cmd)
		return nil
	}, func(res Result) {
		localResults = append(localResults, res)
	})

	if len(sentCommands) != 0 {
		t.Fatalf("exec trigger sent commands to MUD: %v", sentCommands)
	}
	if len(localResults) != 1 || localResults[0].Kind != ResultExec {
		t.Fatalf("exec trigger local results = %+v, want one ResultExec", localResults)
	}
	if localResults[0].Text != "./items_db_client" || !reflect.DeepEqual(localResults[0].Args, []string{"red;blue"}) {
		t.Fatalf("exec trigger argv = path %q args %#v", localResults[0].Text, localResults[0].Args)
	}
}

func TestApplyEffectsButtonWithoutStore(t *testing.T) {
	v := New(nil, 1)
	effects := []Effect{{Type: "button", Label: "press", Command: "look"}}

	buttons, variablesChanged := v.ApplyEffects(effects, 123, "main", nil, nil)

	if variablesChanged {
		t.Fatalf("variablesChanged = true, want false")
	}
	if len(buttons) != 1 || buttons[0].Label != "press" {
		t.Fatalf("buttons = %+v, want one button effect", buttons)
	}
}

func TestArcticTriggerCaptureInCommand(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) pants heavily\.`, Command: "cast 'refresh' %1", Enabled: true},
	}

	effects, _ := v.MatchTriggers("Воин pants heavily.")
	if len(effects) != 1 {
		t.Fatalf("refresh trigger = %d, want 1", len(effects))
	}
	if effects[0].Command != "cast 'refresh' %1" {
		t.Errorf("capture in command = %q, want %q", effects[0].Command, "cast 'refresh' %1")
	}
}

func TestArcticTriggerCancelStand(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You should probably stand up!`, Command: "cancel;stand", Enabled: true},
	}

	effects, _ := v.MatchTriggers("You should probably stand up!")
	if len(effects) != 1 {
		t.Fatalf("cancel+stand trigger = %d, want 1", len(effects))
	}

	commands := v.ProcessInput(effects[0].Command)
	if len(commands) != 2 || commands[0] != "cancel" || commands[1] != "stand" {
		t.Errorf("cancel;stand expansion = %v, want [cancel, stand]", commands)
	}
}

func TestTriggerVarInPatternUndefinedExpandsEmpty(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^$lider сказал$`, Command: "echo matched", Enabled: true},
	}
	v.ensureFresh()

	effects, _ := v.MatchTriggers(` сказал`)
	if len(effects) != 1 {
		t.Fatalf("undefined $lider should expand to empty string and match empty prefix, got %d effects", len(effects))
	}

	effects, _ = v.MatchTriggers(`$lider сказал`)
	if len(effects) != 0 {
		t.Errorf("undefined $lider should not match literal '$lider', got %d effects", len(effects))
	}
}

func TestTriggerVarInPatternWithVarDefined(t *testing.T) {
	v := New(nil, 1)
	v.variables["lider"] = "Игрок"
	v.triggers = []storage.TriggerRule{
		{Pattern: `^$lider сказа(л|ла) группе: "сост"$`, Command: "echo matched", Enabled: true},
	}
	v.ensureFresh()

	effects, _ := v.MatchTriggers(`Игрок сказал группе: "сост"`)
	if len(effects) != 1 {
		t.Fatalf("trigger with $lider=Игрок should match, got %d effects", len(effects))
	}
	if effects[0].Command != "echo matched" {
		t.Errorf("command = %q, want %q", effects[0].Command, "echo matched")
	}

	effects, _ = v.MatchTriggers(`Игрок сказала группе: "сост"`)
	if len(effects) != 1 {
		t.Errorf("said 'сказала' should also match (л|ла), got %d effects", len(effects))
	}

	effects, _ = v.MatchTriggers(`Босс сказал группе: "сост"`)
	if len(effects) != 0 {
		t.Errorf("should NOT match with wrong name, got %d effects", len(effects))
	}
}

func TestTriggerVarInPatternViaActionCommand(t *testing.T) {
	v := New(nil, 1)
	v.variables["lider"] = "Босс"

	v.dispatchCommand("#action {^$lider сказа(л|ла) группе: \"сост\"$} {echo ok}", 0, nil)
	if len(v.triggers) != 1 {
		t.Fatalf("expected one trigger, got %d", len(v.triggers))
	}
	if v.triggers[0].Pattern != `^$lider сказа(л|ла) группе: "сост"$` {
		t.Fatalf("#action should preserve pattern template, got %q", v.triggers[0].Pattern)
	}
	v.ensureFresh()

	effects, _ := v.MatchTriggers(`Босс сказал группе: "сост"`)
	if len(effects) != 1 {
		t.Fatalf("trigger via #action with $lider=Босс should match, got %d effects", len(effects))
	}
}

func TestTriggerVarInPatternRebuildsAfterVariableChange(t *testing.T) {
	v := New(nil, 1)
	v.variables["lider"] = "Игрок"
	v.triggers = []storage.TriggerRule{
		{Pattern: `^$lider сказал$`, Command: "echo matched", Enabled: true},
	}

	effects, _ := v.MatchTriggers(`Игрок сказал`)
	if len(effects) != 1 {
		t.Fatalf("initial $lider=Игрок should match, got %d effects", len(effects))
	}

	v.ProcessInputDetailed("#variable {lider} {Босс}")

	effects, _ = v.MatchTriggers(`Босс сказал`)
	if len(effects) != 1 {
		t.Fatalf("updated $lider=Босс should match, got %d effects", len(effects))
	}
	effects, _ = v.MatchTriggers(`Игрок сказал`)
	if len(effects) != 0 {
		t.Fatalf("old $lider=Игрок should no longer match, got %d effects", len(effects))
	}
}

func TestTriggerVarInPatternQuotesVariableLiteral(t *testing.T) {
	v := New(nil, 1)
	v.variables["lider"] = "A.B"
	v.triggers = []storage.TriggerRule{
		{Pattern: `^$lider says$`, Command: "echo matched", Enabled: true},
	}

	effects, _ := v.MatchTriggers(`A.B says`)
	if len(effects) != 1 {
		t.Fatalf("literal variable value should match, got %d effects", len(effects))
	}

	effects, _ = v.MatchTriggers(`AxB says`)
	if len(effects) != 0 {
		t.Fatalf("quoted variable value should not act as regex wildcard, got %d effects", len(effects))
	}
}

func TestTriggerVarInPatternReloadFromStoreVariableChange(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)

	if err := store.SaveTrigger(1, `^$lider сказал$`, "echo matched", false, "default"); err != nil {
		t.Fatalf("SaveTrigger: %v", err)
	}
	if err := store.SetVariable(1, "lider", "Игрок"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	if err := v.ReloadFromStore(); err != nil {
		t.Fatalf("ReloadFromStore: %v", err)
	}

	effects, _ := v.MatchTriggers(`Игрок сказал`)
	if len(effects) != 1 {
		t.Fatalf("stored $lider=Игрок should match, got %d effects", len(effects))
	}

	if err := store.SetVariable(1, "lider", "Босс"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	if err := v.ReloadFromStore(); err != nil {
		t.Fatalf("ReloadFromStore after variable change: %v", err)
	}

	effects, _ = v.MatchTriggers(`Босс сказал`)
	if len(effects) != 1 {
		t.Fatalf("stored $lider=Босс should match after reload, got %d effects", len(effects))
	}
	effects, _ = v.MatchTriggers(`Игрок сказал`)
	if len(effects) != 0 {
		t.Fatalf("old stored $lider=Игрок should no longer match after reload, got %d effects", len(effects))
	}
}

func TestActionVariableInPatternAndCommandBody(t *testing.T) {
	v := New(nil, 1)
	v.ProcessInputDetailed("#variable {mob} {Гоблин}")
	v.ProcessInputDetailed("#action {$mob игнорирует} {миг $mob}")

	if len(v.triggers) != 1 {
		t.Fatalf("expected one trigger, got %d", len(v.triggers))
	}
	if v.triggers[0].Pattern != `$mob игнорирует` {
		t.Fatalf("#action should preserve pattern template, got %q", v.triggers[0].Pattern)
	}
	if v.triggers[0].Command != `миг $mob` {
		t.Fatalf("#action should preserve command template, got %q", v.triggers[0].Command)
	}

	effects, _ := v.MatchTriggers("Гоблин игнорирует")
	if len(effects) != 1 {
		t.Fatalf("trigger with $mob=Гоблин should match, got %d effects", len(effects))
	}

	var sent []string
	v.ApplyEffects(effects, 0, "main", func(cmd string, _ int64, _ string) error {
		sent = append(sent, cmd)
		return nil
	}, func(Result) {})
	if len(sent) != 1 || sent[0] != "миг Гоблин" {
		t.Fatalf("ApplyEffects sent %v, want [миг Гоблин]", sent)
	}

	v.ProcessInputDetailed("#variable {mob} {Орк}")
	effects, _ = v.MatchTriggers("Орк игнорирует")
	if len(effects) != 1 {
		t.Fatalf("trigger should rebuild for updated $mob=Орк, got %d effects", len(effects))
	}
	sent = nil
	v.ApplyEffects(effects, 0, "main", func(cmd string, _ int64, _ string) error {
		sent = append(sent, cmd)
		return nil
	}, func(Result) {})
	if len(sent) != 1 || sent[0] != "миг Орк" {
		t.Fatalf("ApplyEffects after $mob update sent %v, want [миг Орк]", sent)
	}

	effects, _ = v.MatchTriggers("Гоблин игнорирует")
	if len(effects) != 0 {
		t.Fatalf("old $mob=Гоблин should no longer match, got %d effects", len(effects))
	}
}
