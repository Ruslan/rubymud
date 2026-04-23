package vm

import (
	"runtime"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

func TestTTSCommand(t *testing.T) {
	v := New(nil, 1)

	// Mock TTS to prevent actual sound
	var spokenText string
	v.ttsFn = func(t string) {
		spokenText = t
	}

	// Usage message
	results := v.ProcessInputDetailed("#tts")
	if len(results) != 1 || results[0].Text != "#tts: usage: #tts {text}" {
		t.Errorf("expected usage message, got %+v", results)
	}

	// Execution
	results = v.ProcessInputDetailed("#tts {ready}")
	if runtime.GOOS == "darwin" {
		if len(results) != 0 {
			t.Errorf("expected silent success on macOS, got %+v", results)
		}
		if spokenText != "ready" {
			t.Errorf("expected spoken text 'ready', got %q", spokenText)
		}
	} else {
		if len(results) == 0 {
			t.Errorf("expected support message on %s, got nothing", runtime.GOOS)
		}
	}
}

func TestNopCommand(t *testing.T) {
	v := New(nil, 1)
	result := v.ProcessInput("#nop this is a comment")
	if len(result) != 0 {
		t.Errorf("ProcessInput(#nop) = %v, want empty", result)
	}
}

func TestRepeatSyntax(t *testing.T) {
	v := New(nil, 1)
	result := v.ProcessInput("#3 север")
	expected := []string{"север", "север", "север"}
	if len(result) != len(expected) {
		t.Errorf("ProcessInput(#3 север) = %v, want %v", result, expected)
	}
	for i := range result {
		if result[i] != expected[i] {
			t.Errorf("result[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestRepeatWithAlias(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {кул} {удар}", 0)

	result := v.ProcessInput("#3 кул")
	if len(result) != 3 {
		t.Fatalf("#3 кул = %d commands, want 3: %v", len(result), result)
	}
	for _, cmd := range result {
		if cmd != "удар" {
			t.Errorf("repeat+alias command = %q, want 'удар'", cmd)
		}
	}
}

func TestCmdAliasBracesStripped(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {тест11} {смо деву}", 0)

	template := ""
	for _, a := range v.aliases {
		if a.Name == "тест11" {
			template = a.Template
		}
	}
	if template != "смо деву" {
		t.Errorf("alias template should be 'смо деву', got %q", template)
	}

	expanded := v.ProcessInput("тест11")
	if len(expanded) != 1 || expanded[0] != "смо деву" {
		t.Errorf("alias expansion should be 'смо деву', got %v", expanded)
	}
}

func TestAliasRecursiveLocalCommands(t *testing.T) {
	v := New(nil, 1)

	// Define a complex alias that updates a variable and outputs a local message to a specific buffer
	v.dispatchCommand("#alias {ц1} {#var t1 %1; #woutput {vars} {[$TIME]: set t1 = $t1}}", 0)

	// Execute the alias
	results := v.ProcessInputDetailed("ц1 орк")

	// Verify the variable was updated
	if val := v.variables["t1"]; val != "орк" {
		t.Errorf("expected variable t1 to be 'орк', got %q", val)
	}

	// Verify the output was routed correctly and nothing was sent to the server
	// We expect 2 results:
	// 1. The automatic echo from #var: "#variable {t1} = {орк}"
	// 2. Our custom #woutput echo
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	varRes := results[0]
	if varRes.Kind != ResultEcho || varRes.Text != "#variable {t1} = {орк}" {
		t.Errorf("unexpected var result: %+v", varRes)
	}

	woutRes := results[1]
	if woutRes.Kind != ResultEcho {
		t.Errorf("expected kind to be echo for woutput, got %s", woutRes.Kind)
	}
	if woutRes.TargetBuffer != "vars" {
		t.Errorf("expected target buffer to be 'vars', got %q", woutRes.TargetBuffer)
	}

	// The text should contain the evaluated variable and the $TIME builtin
	// $TIME format is HH:MM:SS, so we just check for the static parts
	if woutRes.Text[0] != '[' || !strings.HasSuffix(woutRes.Text, "]: set t1 = орк") {
		t.Errorf("unexpected woutput text: %q", woutRes.Text)
	}
}

func TestProcessInput(t *testing.T) {
	v := New(nil, 1)
	v.aliases = []storage.AliasRule{
		{Name: "уу", Template: "у %1;пари", Enabled: true},
		{Name: "сняя", Template: "сня %1;пол %1 $сумка", Enabled: true},
		{Name: "моддву", Template: "взя $двуруч $сумка;дву $двуруч", Enabled: true},
	}
	v.variables["двуруч"] = "фламберг"
	v.variables["сумка"] = "сумк"

	tests := []struct {
		input    string
		expected []string
	}{
		{"уу крыса", []string{"у крыса", "пари"}},
		{"сняя кольцо", []string{"сня кольцо", "пол кольцо сумк"}},
		{"моддву", []string{"взя фламберг сумк", "дву фламберг"}},
		{"обычная команда", []string{"обычная команда"}},
	}

	for _, tt := range tests {
		result := v.ProcessInput(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("ProcessInput(%q) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("ProcessInput(%q)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}

func TestSplitBraceArg(t *testing.T) {
	tests := []struct {
		input string
		arg   string
		rest  string
	}{
		{"{name} {template}", "name", "{template}"},
		{"'name' 'template'", "name", "'template'"},
		{`"name" "template"`, "name", `"template"`},
		{"name template", "name", "template"},
		{"{a b} {c d}", "a b", "{c d}"},
		{"", "", ""},
	}

	for _, tt := range tests {
		arg, rest := splitBraceArg(tt.input)
		if arg != tt.arg || rest != tt.rest {
			t.Errorf("splitBraceArg(%q) = (%q, %q), want (%q, %q)", tt.input, arg, rest, tt.arg, tt.rest)
		}
	}
}

func TestArcticLootallAlias(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {lootall} {get all corpse;get all 2.corpse;get all 3.corpse;get all 4.corpse}", 0)

	result := v.ProcessInput("lootall")
	expected := []string{
		"get all corpse",
		"get all 2.corpse",
		"get all 3.corpse",
		"get all 4.corpse",
	}
	if len(result) != len(expected) {
		t.Fatalf("lootall expansion got %d commands, want %d: %v", len(result), len(expected), result)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("lootall[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestArcticFeacuAlias(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {feacu} {fea;order all.elemental get all vine;order all.elemental drop all.grapes;order all.elemental eat all}", 0)

	result := v.ProcessInput("feacu")
	if len(result) != 4 {
		t.Fatalf("feacu expansion got %d commands, want 4: %v", len(result), result)
	}
	if result[0] != "fea" {
		t.Errorf("feacu[0] = %q, want %q", result[0], "fea")
	}
	if result[1] != "order all.elemental get all vine" {
		t.Errorf("feacu[1] = %q, want %q", result[1], "order all.elemental get all vine")
	}
}

func TestArcticCastAliasWithQuotes(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {mm} {cast 'magic missile'}", 0)

	result := v.ProcessInput("mm")
	if len(result) != 1 || result[0] != "cast 'magic missile'" {
		t.Errorf("mm expansion = %v, want [cast 'magic missile']", result)
	}
}

func TestArcticAliasWithArgs(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {hr} {cast 'regenerate' %1}", 0)

	result := v.ProcessInput("hr крыса")
	if len(result) != 1 || result[0] != "cast 'regenerate' крыса" {
		t.Errorf("hr expansion = %v, want [cast 'regenerate' крыса]", result)
	}
}

func TestArcticCastAliasWithSingleWordArg(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {ab} {cast 'acid blast'}", 0)

	result := v.ProcessInput("ab")
	if len(result) != 1 || result[0] != "cast 'acid blast'" {
		t.Errorf("ab = %v, want [cast 'acid blast']", result)
	}
}

func TestArcticBrewscribeAlias(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {brewscribe} {rest;brew 'cure light';scribe 'cu l';stand}", 0)

	result := v.ProcessInput("brewscribe")
	expected := []string{"rest", "brew 'cure light'", "scribe 'cu l'", "stand"}
	if len(result) != len(expected) {
		t.Fatalf("brewscribe = %d commands, want %d: %v", len(result), len(expected), result)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("brewscribe[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestArcticCompAlias(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {comp} {get all.component;put all.component pouch}", 0)

	result := v.ProcessInput("comp")
	if len(result) != 2 {
		t.Fatalf("comp = %d commands, want 2: %v", len(result), result)
	}
	if result[0] != "get all.component" {
		t.Errorf("comp[0] = %q, want 'get all.component'", result[0])
	}
	if result[1] != "put all.component pouch" {
		t.Errorf("comp[1] = %q, want 'put all.component pouch'", result[1])
	}
}

func TestSpeedwalkBasic(t *testing.T) {
	v := New(nil, 1)
	result := v.ProcessInput("nsewud")
	expected := []string{"n", "s", "e", "w", "u", "d"}
	if len(result) != len(expected) {
		t.Fatalf("nsewud = %d, want %d: %v", len(result), len(expected), result)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("nsewud[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestSpeedwalkWithCount(t *testing.T) {
	v := New(nil, 1)
	result := v.ProcessInput("3nw2e")
	expected := []string{"n", "n", "n", "w", "e", "e"}
	if len(result) != len(expected) {
		t.Fatalf("3nw2e = %d, want %d: %v", len(result), len(expected), result)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("3nw2e[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestSpeedwalkComplex(t *testing.T) {
	v := New(nil, 1)
	result := v.ProcessInput("2n3e4wu")
	expected := []string{"n", "n", "e", "e", "e", "w", "w", "w", "w", "u"}
	if len(result) != len(expected) {
		t.Fatalf("2n3e4wu = %d, want %d: %v", len(result), len(expected), result)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("2n3e4wu[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestSpeedwalkNotTriggered(t *testing.T) {
	v := New(nil, 1)

	result := v.ProcessInput("look")
	if len(result) != 1 || result[0] != "look" {
		t.Errorf("non-speedwalk input should pass through, got %v", result)
	}

	result = v.ProcessInput("n s")
	if len(result) != 1 || result[0] != "n s" {
		t.Errorf("input with spaces should not be speedwalk, got %v", result)
	}

	result = v.ProcessInput("say north")
	if len(result) != 1 || result[0] != "say north" {
		t.Errorf("'say north' should not be speedwalk, got %v", result)
	}
}

func TestSpeedwalkSingleDir(t *testing.T) {
	v := New(nil, 1)
	result := v.ProcessInput("n")
	if len(result) != 1 || result[0] != "n" {
		t.Errorf("single n = %v, want [n]", result)
	}
}
