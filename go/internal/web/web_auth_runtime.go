package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"rubymud/go/internal/storage"
)

const webAuthExternalReason = "Web access credentials are managed by MUDHOST_BASIC_AUTH_USER and MUDHOST_BASIC_AUTH_PASS."

type webAuthRuntime struct {
	mu              sync.RWMutex
	authMu          sync.Mutex
	external        bool
	persisted       storage.WebAuthSettings
	snapshot        webAuthSnapshot
	comparePassword func([]byte, []byte) error
}

type webAuthSnapshot struct {
	enabled      bool
	username     string
	passwordHash string
	cachedDigest [sha256.Size]byte
	hasCache     bool
}

func newWebAuthRuntime(persisted storage.WebAuthSettings, external BasicAuth) (*webAuthRuntime, error) {
	if err := external.validate(); err != nil {
		return nil, err
	}
	runtime := &webAuthRuntime{persisted: persisted, external: external.enabled(), comparePassword: bcrypt.CompareHashAndPassword}
	if runtime.external {
		hash, err := bcrypt.GenerateFromPassword([]byte(external.Pass), bcrypt.DefaultCost)
		if err != nil {
			return nil, errors.New("hash external HTTP basic auth password")
		}
		runtime.snapshot = webAuthSnapshot{enabled: true, username: external.User, passwordHash: string(hash)}
		return runtime, nil
	}
	if err := runtime.rebuildLocked(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *webAuthRuntime) rebuildLocked() error {
	state := webAuthSnapshot{
		enabled:      r.persisted.Enabled,
		username:     r.persisted.Username,
		passwordHash: r.persisted.PasswordHash,
	}
	if state.enabled {
		if state.username == "" || state.passwordHash == "" {
			return errors.New("stored web access protection is enabled without credentials")
		}
		if !validWebAuthPasswordHash(state.passwordHash) {
			return errors.New("stored web access password hash is invalid")
		}
	}
	r.snapshot = state
	return nil
}

func (r *webAuthRuntime) replacePersisted(settings storage.WebAuthSettings) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if settings.Revision <= r.persisted.Revision {
		return nil
	}
	if r.external {
		r.persisted = settings
		return nil
	}
	previous := r.persisted
	r.persisted = settings
	if err := r.rebuildLocked(); err != nil {
		r.persisted = previous
		return err
	}
	return nil
}

func (r *webAuthRuntime) authenticate(user, password string, present bool) bool {
	r.mu.RLock()
	snapshot := r.snapshot
	r.mu.RUnlock()
	if !snapshot.enabled {
		return true
	}
	digest := webAuthCredentialDigest(user, password)
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(snapshot.username)) == 1
	// Do not spend an adaptive-hash operation on an absent credential or a
	// username which cannot authenticate. Besides being faster for browsers,
	// this prevents anonymous requests from turning into a bcrypt work queue.
	if !present || !userOK {
		return false
	}
	cacheOK := snapshot.hasCache && subtle.ConstantTimeCompare(digest[:], snapshot.cachedDigest[:]) == 1
	if cacheOK {
		return true
	}

	// A cold page opens several API requests in parallel. Permit one adaptive
	// check to fill the cache, but fail concurrent cache misses instead of
	// queuing attacker-controlled bcrypt work behind it.
	if !r.authMu.TryLock() {
		return false
	}
	defer r.authMu.Unlock()
	r.mu.RLock()
	snapshot = r.snapshot
	r.mu.RUnlock()
	if !snapshot.enabled {
		return true
	}
	userOK = subtle.ConstantTimeCompare([]byte(user), []byte(snapshot.username)) == 1
	if !present || !userOK {
		return false
	}
	cacheOK = snapshot.hasCache && subtle.ConstantTimeCompare(digest[:], snapshot.cachedDigest[:]) == 1
	if cacheOK {
		return true
	}
	passwordOK := r.comparePassword([]byte(snapshot.passwordHash), []byte(password)) == nil
	if !passwordOK {
		return false
	}

	r.mu.Lock()
	if r.snapshot.enabled && r.snapshot.username == snapshot.username && r.snapshot.passwordHash == snapshot.passwordHash {
		r.snapshot.cachedDigest = digest
		r.snapshot.hasCache = true
	}
	r.mu.Unlock()
	return true
}

func webAuthCredentialDigest(username, password string) [sha256.Size]byte {
	credential := make([]byte, 0, len(username)+1+len(password))
	credential = append(credential, username...)
	credential = append(credential, 0)
	credential = append(credential, password...)
	return sha256.Sum256(credential)
}

func (r *webAuthRuntime) persistedValue() storage.WebAuthSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.persisted
}

func (r *webAuthRuntime) status() webAuthSettingsJSON {
	r.mu.RLock()
	defer r.mu.RUnlock()
	username := r.persisted.Username
	credentialConfigured := username != "" && validWebAuthPasswordHash(r.persisted.PasswordHash)
	if r.external {
		username = r.snapshot.username
		credentialConfigured = true
	}
	return webAuthSettingsJSON{
		Enabled:              r.persisted.Enabled,
		RequestedEnabled:     r.persisted.Enabled || r.external,
		EffectiveEnabled:     r.snapshot.enabled,
		Username:             username,
		CredentialConfigured: credentialConfigured,
		ExternallyManaged:    r.external,
		ExternallyManagedReason: func() string {
			if r.external {
				return webAuthExternalReason
			}
			return ""
		}(),
	}
}

func validWebAuthPasswordHash(hash string) bool {
	_, err := bcrypt.Cost([]byte(hash))
	return err == nil
}

func (b BasicAuth) validate() error {
	if (b.User == "") != (b.Pass == "") {
		return errors.New("MUDHOST_BASIC_AUTH_USER and MUDHOST_BASIC_AUTH_PASS must be set together")
	}
	if !b.enabled() {
		return nil
	}
	if strings.ContainsRune(b.User, ':') {
		return errors.New("MUDHOST_BASIC_AUTH_USER must not contain ':'")
	}
	if len(b.Pass) > 72 {
		return errors.New("MUDHOST_BASIC_AUTH_PASS must be at most 72 bytes")
	}
	return nil
}
