package web

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"rubymud/go/internal/storage"
)

func TestWebAuthCachesSuccessfulAdaptiveHashCheck(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	runtime, err := newWebAuthRuntime(storage.WebAuthSettings{
		Enabled:      true,
		Username:     "operator",
		PasswordHash: string(hash),
	}, BasicAuth{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	checks := 0
	runtime.comparePassword = func(hash, password []byte) error {
		checks++
		return bcrypt.CompareHashAndPassword(hash, password)
	}

	for range 3 {
		if !runtime.authenticate("operator", "correct horse battery staple", true) {
			t.Fatal("correct credentials were rejected")
		}
	}
	if checks != 1 {
		t.Fatalf("adaptive password checks = %d, want 1", checks)
	}

	if runtime.authenticate("operator", "wrong password", true) {
		t.Fatal("wrong password was accepted")
	}
	if checks != 2 {
		t.Fatalf("adaptive password checks after miss = %d, want 2", checks)
	}
}

func TestWebAuthRejectsAnonymousAndWrongUserWithoutBcryptOrQueueing(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	runtime, err := newWebAuthRuntime(storage.WebAuthSettings{
		Enabled: true, Username: "operator", PasswordHash: string(hash),
	}, BasicAuth{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	checks := 0
	runtime.comparePassword = func(_, _ []byte) error {
		checks++
		return nil
	}
	if runtime.authenticate("", "", false) || runtime.authenticate("attacker", "password", true) {
		t.Fatal("anonymous or wrong username authenticated")
	}
	if checks != 0 {
		t.Fatalf("bcrypt checks for anonymous/wrong user = %d, want 0", checks)
	}

	runtime.authMu.Lock()
	defer runtime.authMu.Unlock()
	done := make(chan bool, 1)
	go func() { done <- runtime.authenticate("operator", "wrong password", true) }()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("concurrent cache miss authenticated")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("concurrent cache miss queued behind bcrypt work")
	}
	if checks != 0 {
		t.Fatalf("bcrypt checks while gate held = %d, want 0", checks)
	}
}

func TestWebAuthCredentialUpdateInvalidatesFastCache(t *testing.T) {
	firstHash, _ := bcrypt.GenerateFromPassword([]byte("first secure password"), bcrypt.MinCost)
	secondHash, _ := bcrypt.GenerateFromPassword([]byte("second secure password"), bcrypt.MinCost)
	runtime, err := newWebAuthRuntime(storage.WebAuthSettings{
		Enabled:      true,
		Username:     "operator",
		PasswordHash: string(firstHash),
		Revision:     1,
	}, BasicAuth{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	if !runtime.authenticate("operator", "first secure password", true) {
		t.Fatal("first credentials were rejected")
	}
	if err := runtime.replacePersisted(storage.WebAuthSettings{
		Enabled:      true,
		Username:     "operator",
		PasswordHash: string(secondHash),
		Revision:     2,
	}); err != nil {
		t.Fatalf("replace settings: %v", err)
	}
	if runtime.authenticate("operator", "first secure password", true) {
		t.Fatal("cached old credentials survived rotation")
	}
	if !runtime.authenticate("operator", "second secure password", true) {
		t.Fatal("new credentials were rejected")
	}
}
