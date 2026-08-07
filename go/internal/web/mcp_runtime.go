package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"

	"rubymud/go/internal/storage"
)

// mcpRuntime owns the request-time gate. Requests take one immutable snapshot,
// so no MCP request reads SQLite and an accepted request has stable credentials
// for its lifetime. Configuration updates replace the snapshot atomically.
type mcpRuntime struct {
	mu         sync.RWMutex
	loopback   bool
	external   MCPConfig
	persisted  storage.MCPSettings
	snapshot   mcpRuntimeSnapshot
	generation uint64
}

type mcpRuntimeSnapshot struct {
	enabled     bool
	tokenDigest [sha256.Size]byte
	hasToken    bool
	generation  uint64
}

func newMCPRuntime(listenAddr string, persisted storage.MCPSettings, external MCPConfig) (*mcpRuntime, error) {
	external = external.normalized()
	if external.tokenExternallyManaged() && external.Token != "" && !validBearerToken(external.Token) {
		return nil, errors.New("MCP token is not a valid Bearer token")
	}
	r := &mcpRuntime{loopback: mcpListenerIsLoopback(listenAddr), external: external, persisted: persisted}
	if err := r.rebuildLocked(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *mcpRuntime) snapshotValue() mcpRuntimeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

// lockSnapshot establishes the request's dispatch linearization point. Callers
// release it before running tools or doing any database or network work.
func (r *mcpRuntime) lockSnapshot() (mcpRuntimeSnapshot, func()) {
	r.mu.RLock()
	return r.snapshot, r.mu.RUnlock
}

func (r *mcpRuntime) replacePersisted(persisted storage.MCPSettings) (changed bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if persisted.Revision <= r.persisted.Revision {
		return false, nil
	}
	before := r.snapshot
	r.persisted = persisted
	if err := r.rebuildLocked(); err != nil {
		return false, err
	}
	return before.generation != r.snapshot.generation, nil
}

func (r *mcpRuntime) rebuildLocked() error {
	state := mcpRuntimeSnapshot{}
	enabled := r.persisted.Enabled
	if r.external.enabledExternallyManaged() {
		enabled = r.external.Enabled
	}
	digest := r.persisted.TokenDigest
	if r.external.tokenExternallyManaged() {
		if r.external.Token != "" {
			state.tokenDigest = sha256.Sum256([]byte(r.external.Token))
			state.hasToken = true
		}
	} else if digest != "" {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			// A corrupt or legacy value must never downgrade a remote listener to
			// tokenless access. Keep the endpoint closed until an admin rotates it.
			enabled = false
		} else {
			copy(state.tokenDigest[:], decoded)
			state.hasToken = true
		}
	}
	if enabled && !state.hasToken && !r.loopback {
		enabled = false
	}
	state.enabled = enabled
	if r.generation == 0 || state.enabled != r.snapshot.enabled || state.hasToken != r.snapshot.hasToken || state.tokenDigest != r.snapshot.tokenDigest {
		r.generation++
	}
	state.generation = r.generation
	r.snapshot = state
	return nil
}

func (r *mcpRuntime) enabledExternallyManaged() bool { return r.external.enabledExternallyManaged() }
func (r *mcpRuntime) tokenExternallyManaged() bool   { return r.external.tokenExternallyManaged() }

func (r *mcpRuntime) persistedValue() storage.MCPSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.persisted
}

func (r *mcpRuntime) status() mcpSettingsJSON {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reasons := make([]string, 0, 2)
	if r.external.enabledExternallyManaged() && r.external.tokenExternallyManaged() {
		reasons = append(reasons, "MCP enabled state and credential are managed by startup configuration.")
	} else if r.external.enabledExternallyManaged() {
		reasons = append(reasons, "MCP enabled state is managed by startup configuration.")
	} else if r.external.tokenExternallyManaged() {
		reasons = append(reasons, "MCP credential is managed by startup configuration.")
	}
	requestedEnabled := r.persisted.Enabled
	if r.external.enabledExternallyManaged() {
		requestedEnabled = r.external.Enabled
	}
	if requestedEnabled && !r.snapshot.enabled && !r.snapshot.hasToken && !r.loopback {
		reasons = append(reasons, "MCP is inactive because this listener requires a credential.")
	}
	return mcpSettingsJSON{
		Enabled:                  r.persisted.Enabled,
		RequestedEnabled:         requestedEnabled,
		EffectiveEnabled:         r.snapshot.enabled,
		TokenConfigured:          r.snapshot.hasToken,
		ExternallyManaged:        r.external.enabledExternallyManaged() || r.external.tokenExternallyManaged(),
		ExternallyManagedReason:  strings.Join(reasons, " "),
		EnabledExternallyManaged: r.external.enabledExternallyManaged(),
		TokenExternallyManaged:   r.external.tokenExternallyManaged(),
		ListenerExposure:         listenerExposure(r.loopback),
	}
}

func mcpListenerIsLoopback(listenAddr string) bool {
	host, _, err := net.SplitHostPort(listenAddr)
	return err == nil && isLoopbackHost(host)
}

func mcpListenerExposure(listenAddr string) string {
	return listenerExposure(mcpListenerIsLoopback(listenAddr))
}

func listenerExposure(loopback bool) string {
	if loopback {
		return "loopback"
	}
	return "network"
}

func (c MCPConfig) enabledExternallyManaged() bool { return c.EnabledSet || c.Enabled }
func (c MCPConfig) tokenExternallyManaged() bool   { return c.TokenSet || c.Token != "" }

func (c MCPConfig) normalized() MCPConfig {
	c.Token = strings.TrimSpace(c.Token)
	return c
}
