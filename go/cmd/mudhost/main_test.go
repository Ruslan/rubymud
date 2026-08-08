package main

import (
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"rubymud/go/internal/buildinfo"
	"rubymud/go/internal/web"
)

func TestVersionLineUsesBuildMetadata(t *testing.T) {
	previous := buildinfo.Version
	buildinfo.Version = "v9.8.7-test"
	t.Cleanup(func() { buildinfo.Version = previous })

	if got := versionLine(); got != "mudhost v9.8.7-test" {
		t.Fatalf("versionLine() = %q", got)
	}
}

func testEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestResolveMCPConfigPrecedenceAndWhitespace(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		cliEnabled optionalBool
		cliToken   optionalString
		want       web.MCPConfig
	}{
		{name: "default disabled", env: map[string]string{}, want: web.MCPConfig{}},
		{name: "environment", env: map[string]string{"MUDHOST_MCP_ENABLED": "true", "MUDHOST_MCP_TOKEN": "  env-token  "}, want: web.MCPConfig{Enabled: true, Token: "env-token", EnabledSet: true, TokenSet: true}},
		{name: "cli overrides environment", env: map[string]string{"MUDHOST_MCP_ENABLED": "true", "MUDHOST_MCP_TOKEN": "env-token"}, cliEnabled: optionalBool{value: false, set: true}, cliToken: optionalString{value: " cli-token ", set: true}, want: web.MCPConfig{Token: "cli-token", EnabledSet: true, TokenSet: true}},
		{name: "explicit empty cli token overrides environment", env: map[string]string{"MUDHOST_MCP_TOKEN": "env-token"}, cliToken: optionalString{value: " \t", set: true}, want: web.MCPConfig{TokenSet: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveMCPConfig(testEnv(tc.env), tc.cliEnabled, tc.cliToken)
			if err != nil {
				t.Fatalf("resolveMCPConfig: %v", err)
			}
			if got != tc.want {
				t.Fatalf("config = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveMCPConfigRejectsInvalidEnabledEnvironment(t *testing.T) {
	for _, value := range []string{"sometimes", "", "   "} {
		if _, err := resolveMCPConfig(testEnv(map[string]string{"MUDHOST_MCP_ENABLED": value}), optionalBool{}, optionalString{}); err == nil {
			t.Fatalf("resolveMCPConfig accepted invalid boolean %q", value)
		}
	}
}

func TestResolveMCPConfigExplicitCLIEnabledOverridesInvalidEnvironment(t *testing.T) {
	got, err := resolveMCPConfig(testEnv(map[string]string{"MUDHOST_MCP_ENABLED": "sometimes"}), optionalBool{value: false, set: true}, optionalString{})
	if err != nil {
		t.Fatalf("resolveMCPConfig: %v", err)
	}
	if got.Enabled {
		t.Fatal("MCP enabled, want explicit CLI false")
	}
}

func TestMCPFlagParsing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		wantValue bool
		wantSet   bool
		wantErr   bool
	}{
		{name: "bare true", args: []string{"--mcp"}, wantValue: true, wantSet: true},
		{name: "explicit false", args: []string{"--mcp=false"}, wantSet: true},
		{name: "invalid", args: []string{"--mcp=sometimes"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("mudhost-test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			var got optionalBool
			fs.Var(&got, "mcp", "Enable MCP")
			err := fs.Parse(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Parse error = %v, wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && (got.value != tc.wantValue || got.set != tc.wantSet) {
				t.Fatalf("parsed = %+v, want value=%v set=%v", got, tc.wantValue, tc.wantSet)
			}
		})
	}
}

func TestMCPTokenFlagDoesNotExposeSecretThroughValueString(t *testing.T) {
	var token optionalString
	if err := token.Set("top-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := token.String(); strings.Contains(got, "top-secret") {
		t.Fatalf("String exposed MCP token: %q", got)
	}
}

func TestRuntimeDataDirUsesResolvedSQLitePath(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		db   string
		want string
	}{
		{name: "disk file URI", db: "file:" + filepath.Join(dir, "mudhost.db") + "?cache=shared", want: dir},
		{name: "memory URI fallback", db: "file:volatile?mode=memory&cache=shared", want: "."},
		{name: "encoded memory URI fallback", db: "file:%3Amemory%3A?cache=shared", want: "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runtimeDataDir(tc.db)
			if err != nil {
				t.Fatalf("runtimeDataDir(%q): %v", tc.db, err)
			}
			if got != tc.want {
				t.Fatalf("runtimeDataDir = %q, want %q", got, tc.want)
			}
		})
	}
}
