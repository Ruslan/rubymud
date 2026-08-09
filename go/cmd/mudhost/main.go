package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	// Embed the IANA timezone database into the binary so per-session zones
	// (e.g. "America/Los_Angeles") resolve even on minimal runtime images that
	// ship no system tzdata (Alpine, scratch, distroless). The binary is a
	// CGO_ENABLED=0 static build, so this keeps it fully self-contained instead
	// of depending on the container having a tzdata package installed.
	_ "time/tzdata"

	"rubymud/go/internal/buildinfo"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/web"
)

type optionalBool struct {
	value bool
	set   bool
}

func (v *optionalBool) String() string {
	return strconv.FormatBool(v.value)
}

func (v *optionalBool) Set(raw string) error {
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	v.value = parsed
	v.set = true
	return nil
}

func (v *optionalBool) IsBoolFlag() bool { return true }

type optionalString struct {
	value string
	set   bool
}

// optionalListenAddress keeps the default separate from an explicit --listen
// so a saved Settings value can own normal starts without hiding operator
// intent. Unlike optionalString it is not secret and may appear in diagnostics.
type optionalListenAddress struct {
	value string
	set   bool
}

func (v *optionalListenAddress) String() string { return v.value }

func (v *optionalListenAddress) Set(raw string) error {
	v.value = raw
	v.set = true
	return nil
}

// String is used by flag diagnostics and must never disclose a parsed secret.
func (v *optionalString) String() string { return "" }

func (v *optionalString) Set(raw string) error {
	v.value = raw
	v.set = true
	return nil
}

func resolveMCPConfig(lookupEnv func(string) (string, bool), cliEnabled optionalBool, cliToken optionalString) (web.MCPConfig, error) {
	config := web.MCPConfig{}
	if raw, ok := lookupEnv("MUDHOST_MCP_ENABLED"); ok && !cliEnabled.set {
		enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return web.MCPConfig{}, fmt.Errorf("parse MUDHOST_MCP_ENABLED: %w", err)
		}
		config.Enabled = enabled
		config.EnabledSet = true
	}
	if raw, ok := lookupEnv("MUDHOST_MCP_TOKEN"); ok {
		config.Token = strings.TrimSpace(raw)
		config.TokenSet = true
	}
	if cliEnabled.set {
		config.Enabled = cliEnabled.value
		config.EnabledSet = true
	}
	if cliToken.set {
		config.Token = strings.TrimSpace(cliToken.value)
		config.TokenSet = true
	}
	return config, nil
}

// runtimeDataDir resolves --db before creating runtime directories. SQLite
// file: DSNs need their physical target parsed first; memory DSNs intentionally
// fall back to the current directory for config exports rather than creating a
// literal directory named "file:".
func runtimeDataDir(db string) (string, error) {
	_, physicalPath, err := storage.ResolveSQLitePath(db)
	if err != nil {
		return "", err
	}
	if physicalPath == "" {
		return ".", nil
	}
	return filepath.Dir(physicalPath), nil
}

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	mudAddr := flag.String("mud", "", "Optional: initial MUD server address in host:port format")
	listenAddr := optionalListenAddress{value: web.DefaultWebListenAddr}
	flag.Var(&listenAddr, "listen", "HTTP listen address (overrides saved Web Access listener)")
	dbPath := flag.String("db", "data/mudhost.db", "SQLite database path")
	configDirPath := flag.String("config-dir", "", "Directory for exported profile .tt files (default: config/ next to --db)")
	migrateOnly := flag.Bool("migrate-only", false, "Run migrations and exit")
	rematerializeEdges := flag.Bool("rematerialize-edges", false,
		"Rebuild every map set's derived topology (map_edges) from its rooms and exit; hand-drawn edges are kept")
	var mcpEnabled optionalBool
	var mcpToken optionalString
	flag.Var(&mcpEnabled, "mcp", "Enable the MCP endpoint (default: MUDHOST_MCP_ENABLED or false)")
	flag.Var(&mcpToken, "mcp-token", "Bearer token for MCP (default: MUDHOST_MCP_TOKEN; empty allows loopback-only MCP)")
	flag.Parse()
	if *showVersion {
		fmt.Println(versionLine())
		return
	}

	mcpConfig, err := resolveMCPConfig(os.LookupEnv, mcpEnabled, mcpToken)
	if err != nil {
		log.Fatalf("MCP configuration: %v", err)
	}
	// Ensure data directory exists. Resolve a SQLite URI before taking its
	// directory; filepath.Dir("file:...") would create the wrong path.
	dataDir, err := runtimeDataDir(*dbPath)
	if err != nil {
		log.Fatalf("resolve database path: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("failed to create data directory %s: %v", dataDir, err)
	}

	// Default config-dir is sibling of the DB file
	if *configDirPath == "" {
		*configDirPath = filepath.Join(dataDir, "config")
	}

	// Ensure config directory exists
	if err := os.MkdirAll(*configDirPath, 0755); err != nil {
		log.Fatalf("failed to create config directory %s: %v", *configDirPath, err)
	}

	store, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	if *migrateOnly {
		log.Println("migrations completed successfully")
		return
	}

	// Repair handle for the one guarantee materializing edges gave up: connectivity
	// used to be re-derived on every index build, so a change to `rooms` healed
	// itself; now it is stored, and a write that bypassed the Go write-path leaves
	// it stale forever and undetected. Nothing runs this automatically — scanning
	// for drift costs a
	// full derivation per set on every start.
	if *rematerializeEdges {
		ids, counts, rerr := store.RematerializeAllMapEdges()
		if rerr != nil {
			log.Fatalf("rematerialize map edges: %v", rerr)
		}
		for _, id := range ids {
			log.Printf("map set %d: %d edges", id, counts[id])
		}
		log.Printf("rematerialized topology for %d map set(s)", len(ids))
		return
	}

	persistedWebAuth, err := store.GetWebAuthSettings()
	if err != nil {
		log.Fatalf("load Web Access settings: %v", err)
	}
	listenerConfig, err := web.ResolveWebListenerConfig(persistedWebAuth.ListenAddr, listenAddr.value, listenAddr.set)
	if err != nil {
		log.Fatalf("Web Access listener configuration: %v", err)
	}
	if err := web.ValidateMCPConfig(listenerConfig.CurrentAddr, mcpConfig); err != nil {
		log.Fatalf("MCP configuration: %v", err)
	}

	manager := session.NewManager(store)

	// If --mud is provided, ensure a default session exists and connect to it
	if *mudAddr != "" {
		host, port, err := splitMudAddr(*mudAddr)
		if err != nil {
			log.Fatalf("invalid mud address: %v", err)
		}

		record, err := store.EnsureDefaultSession(host, port)
		if err != nil {
			log.Fatalf("ensure default session: %v", err)
		}

		if _, err := manager.Connect(record.ID); err != nil {
			log.Printf("failed to connect to initial mud session: %v", err)
		}
	}

	basicAuth := web.BasicAuth{
		User: os.Getenv("MUDHOST_BASIC_AUTH_USER"),
		Pass: os.Getenv("MUDHOST_BASIC_AUTH_PASS"),
	}

	server, err := web.NewWithWebListenerConfig(listenerConfig, manager, store, *configDirPath, basicAuth, mcpConfig)
	if err != nil {
		log.Fatalf("create web server: %v", err)
	}
	if basicAuth.User != "" && basicAuth.Pass != "" {
		log.Printf("HTTP basic auth enabled for user %q", basicAuth.User)
	}
	if listenerConfig.CurrentAddr != listenerConfig.ConfiguredAddr {
		log.Printf("mudhost listener %s is an explicit --listen override; Settings will use %s next start", listenerConfig.CurrentAddr, listenerConfig.ConfiguredAddr)
	}
	if web.WebListenerExposure(listenerConfig.CurrentAddr) == "network" {
		log.Printf("WARNING: Web Access Basic Auth over plain HTTP can expose passwords; use a TLS reverse proxy before network deployment")
	}
	log.Printf("mudhost listening on %s using db %s", listenerConfig.CurrentAddr, *dbPath)
	log.Printf("open in browser: http://%s", listenURL(listenerConfig.CurrentAddr))
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("web server failed: %v", err)
	}
}

func versionLine() string {
	return "mudhost " + buildinfo.Version
}

func listenURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func splitMudAddr(mudAddr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(mudAddr)
	if err != nil {
		return "", 0, err
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}

	return host, port, nil
}
