package storage

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The storage package CANNOT import mapper (mapper imports storage for
// storage.Room), so these tests install a STUB compile gate instead of the real
// mapper.Compile. That is deliberate and sufficient here: what this file proves
// is the STORE's contract — that every write runs the registered gate, that a
// rejected spec never lands, and that a failing gate is reported verbatim.
//
// The stub does NOT go unchecked: mapper_profile_realgate_test.go, next to this
// file, is an EXTERNAL test package (storage_test) — which may import mapper,
// since storage_test -> mapper -> storage does not cycle — and runs the same
// write paths through the REAL mapper.Compile. So "the stub has drifted from the
// compiler" is a caught condition, not a hope.
//
// The stub is registered per-test with the previous value restored, so the
// storage test binary's global cannot leak between tests.
// ---------------------------------------------------------------------------

// stubMapperValidator rejects any spec containing "BROKEN" and accepts the rest.
func stubMapperValidator(t *testing.T) {
	t.Helper()
	mapperSpecMu.RLock()
	prev := mapperSpecValidator
	mapperSpecMu.RUnlock()
	RegisterMapperSpecValidator(func(spec string) error {
		if strings.Contains(spec, "BROKEN") {
			return errors.New(`mapper: profile "x": empty exits guard`)
		}
		return nil
	})
	t.Cleanup(func() { RegisterMapperSpecValidator(prev) })
}

const goodSpec = `{"name":"rmud","exits":{"guard":"[ Exits:","pattern":"^x(.*)$","none_token":"none"}}`

func TestMapperProfileCRUD(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	rows, err := store.ListMapperProfiles()
	if err != nil {
		t.Fatalf("ListMapperProfiles: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a fresh DB must hold NO profiles — the builtin is not a row, got %d", len(rows))
	}

	row, err := store.CreateMapperProfile("  Tortilla  ", goodSpec)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if row.Name != "Tortilla" {
		t.Errorf("name = %q, want the trimmed %q", row.Name, "Tortilla")
	}
	if row.Version != MapperSpecVersion {
		t.Errorf("version = %d, want %d", row.Version, MapperSpecVersion)
	}
	if row.CreatedAt == nil || row.UpdatedAt == nil {
		t.Errorf("timestamps not set: %+v", row)
	}

	got, found, err := store.GetMapperProfile(row.ID)
	if err != nil || !found {
		t.Fatalf("GetMapperProfile: found=%v err=%v", found, err)
	}
	if got.Spec != goodSpec {
		t.Errorf("spec did not round-trip verbatim:\n got %q\nwant %q", got.Spec, goodSpec)
	}

	updated, found, err := store.UpdateMapperProfile(row.ID, "Tortilla 2", goodSpec)
	if err != nil || !found {
		t.Fatalf("UpdateMapperProfile: found=%v err=%v", found, err)
	}
	if updated.Name != "Tortilla 2" || updated.ID != row.ID {
		t.Errorf("update = %+v, want the same row renamed", updated)
	}

	deleted, _, err := store.DeleteMapperProfile(row.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteMapperProfile: deleted=%v err=%v", deleted, err)
	}
	if _, found, _ := store.GetMapperProfile(row.ID); found {
		t.Error("profile still present after delete")
	}
}

// TestMapperProfileMissingIsNotAnError pins the degradation contract: a missing
// row is (false, nil), never an error, because a dangling assignment must let
// the session fall back to the builtin rather than fail.
func TestMapperProfileMissingIsNotAnError(t *testing.T) {
	store := newMapperTestStore(t)
	row, found, err := store.GetMapperProfile(4242)
	if err != nil {
		t.Fatalf("a missing profile must not be an error, got %v", err)
	}
	if found {
		t.Fatalf("found a profile that was never written: %+v", row)
	}
	deleted, _, err := store.DeleteMapperProfile(4242)
	if err != nil {
		t.Fatalf("deleting a missing profile must not error, got %v", err)
	}
	if deleted {
		t.Error("DeleteMapperProfile reported a delete that did not happen")
	}
}

// TestMapperProfileWriteRejectsUncompilableSpec is the invariant the whole
// feature rests on: the DB must never hold a spec that cannot be loaded, on
// EITHER write path.
func TestMapperProfileWriteRejectsUncompilableSpec(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	if _, err := store.CreateMapperProfile("bad", `{"BROKEN":true}`); err == nil {
		t.Fatal("create accepted a spec the compile gate rejected")
	} else if !strings.Contains(err.Error(), "empty exits guard") {
		t.Errorf("create error = %v, want the gate's verbatim message", err)
	}
	if rows, _ := store.ListMapperProfiles(); len(rows) != 0 {
		t.Fatalf("a rejected create must not leave a row, got %d", len(rows))
	}

	ok, err := store.CreateMapperProfile("good", goodSpec)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if _, _, err := store.UpdateMapperProfile(ok.ID, "good", `{"BROKEN":true}`); err == nil {
		t.Fatal("update accepted a spec the compile gate rejected")
	}
	after, _, _ := store.GetMapperProfile(ok.ID)
	if after.Spec != goodSpec {
		t.Errorf("a rejected update must leave the stored spec untouched, got %q", after.Spec)
	}
}

// TestMapperProfileWriteFailsClosedWithoutValidator: with no gate registered
// (a binary linking storage but not mapper) writes must FAIL, not silently store
// an unchecked spec.
func TestMapperProfileWriteFailsClosedWithoutValidator(t *testing.T) {
	store := newMapperTestStore(t)

	mapperSpecMu.RLock()
	prev := mapperSpecValidator
	mapperSpecMu.RUnlock()
	RegisterMapperSpecValidator(nil)
	t.Cleanup(func() { RegisterMapperSpecValidator(prev) })

	if _, err := store.CreateMapperProfile("x", goodSpec); !errors.Is(err, ErrNoMapperSpecValidator) {
		t.Fatalf("create error = %v, want ErrNoMapperSpecValidator", err)
	}
	if rows, _ := store.ListMapperProfiles(); len(rows) != 0 {
		t.Fatalf("nothing may be stored without a gate, got %d rows", len(rows))
	}
}

func TestMapperProfileNameRules(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	if _, err := store.CreateMapperProfile("   ", goodSpec); !errors.Is(err, ErrMapperProfileNameRequired) {
		t.Errorf("blank name error = %v, want ErrMapperProfileNameRequired", err)
	}
	if _, err := store.CreateMapperProfile("dup", goodSpec); err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if _, err := store.CreateMapperProfile("dup", goodSpec); !errors.Is(err, ErrMapperProfileNameTaken) {
		t.Errorf("duplicate name error = %v, want ErrMapperProfileNameTaken", err)
	}
}

// TestSessionMapperProfileAssignment covers the per-session column: NULL by
// default (= the builtin), settable, and clearable.
func TestSessionMapperProfileAssignment(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	sess, err := store.CreateSession("s1", "127.0.0.1", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if ref, err := store.GetSessionMapperFormat(sess.ID); err != nil || ref != "" {
		t.Fatalf("a new session must default to NULL (= the default builtin), got ref=%q err=%v", ref, err)
	}

	prof, err := store.CreateMapperProfile("p", goodSpec)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(sess.ID, MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	if ref, _ := store.GetSessionMapperFormat(sess.ID); ref != MapperFormatDBRef(prof.ID) {
		t.Fatalf("assignment not stored: ref=%q", ref)
	}

	// A builtin reference round-trips through the same column — the whole point of
	// migration 015's TEXT shape is that a compiled-in detector, which is not a
	// row, is nameable at all.
	if err := store.SetSessionMapperFormat(sess.ID, "builtin:diku-en"); err != nil {
		t.Fatalf("SetSessionMapperFormat(builtin): %v", err)
	}
	if ref, _ := store.GetSessionMapperFormat(sess.ID); ref != "builtin:diku-en" {
		t.Fatalf("builtin assignment not stored: ref=%q", ref)
	}
	if err := store.SetSessionMapperFormat(sess.ID, MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}

	ids, err := store.SessionIDsUsingMapperProfile(prof.ID)
	if err != nil {
		t.Fatalf("SessionIDsUsingMapperProfile: %v", err)
	}
	if len(ids) != 1 || ids[0] != sess.ID {
		t.Fatalf("sessions using the profile = %v, want [%d]", ids, sess.ID)
	}

	if err := store.SetSessionMapperFormat(sess.ID, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ref, _ := store.GetSessionMapperFormat(sess.ID); ref != "" {
		t.Errorf("clearing the assignment should leave NULL, got %q", ref)
	}
}

// TestDeleteMapperProfileClearsSessions is the reason DeleteMapperProfile does
// the UPDATE itself instead of trusting ON DELETE SET NULL: SQLite's
// foreign_keys pragma is per-connection and this project sets it once on a
// pooled *gorm.DB, so the FK action may never fire. A session left pointing at a
// deleted id would resolve to "profile not found" on every reconnect.
func TestDeleteMapperProfileClearsSessions(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	prof, err := store.CreateMapperProfile("doomed", goodSpec)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	a, _ := store.CreateSession("a", "127.0.0.1", 4000)
	b, _ := store.CreateSession("b", "127.0.0.1", 4000)
	other, _ := store.CreateMapperProfile("keeper", goodSpec)
	c, _ := store.CreateSession("c", "127.0.0.1", 4000)

	for _, id := range []int64{a.ID, b.ID} {
		if err := store.SetSessionMapperFormat(id, MapperFormatDBRef(prof.ID)); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	if err := store.SetSessionMapperFormat(c.ID, MapperFormatDBRef(other.ID)); err != nil {
		t.Fatalf("assign: %v", err)
	}

	found, affected, err := store.DeleteMapperProfile(prof.ID)
	if err != nil {
		t.Fatalf("DeleteMapperProfile: %v", err)
	}
	if !found {
		t.Fatal("DeleteMapperProfile reported nothing deleted")
	}
	// The affected ids come from INSIDE the delete transaction, so the caller can
	// push a live re-apply to exactly the sessions that were cleared. Collecting
	// them beforehand would miss a session assigned in the intervening window.
	if len(affected) != 2 || affected[0] != a.ID || affected[1] != b.ID {
		t.Errorf("affected = %v, want [%d %d]", affected, a.ID, b.ID)
	}

	for _, id := range []int64{a.ID, b.ID} {
		got, err := store.GetSessionMapperFormat(id)
		if err != nil {
			t.Fatalf("GetSessionMapperFormat: %v", err)
		}
		if got != "" {
			t.Errorf("session %d still references the deleted profile (%q)", id, got)
		}
	}
	// An unrelated session's assignment must survive.
	if got, _ := store.GetSessionMapperFormat(c.ID); got != MapperFormatDBRef(other.ID) {
		t.Errorf("unrelated assignment clobbered: ref=%q", got)
	}
}

// TestAssignSessionMapperFormatChecksBothInOneStatement covers the TOCTOU the
// separate check-then-write had: a DELETE landing between "does this profile
// exist?" and "write the column" left the session pointing at a vanished row
// FOREVER. The foreign key never saved us — with the pragma unreliable on a
// pooled connection a dangling id inserted and read back perfectly happily, as
// this test demonstrates — and after migration 015 there is no FK at all, so the
// EXISTS-inside-the-UPDATE is now the ONLY protection.
func TestAssignSessionMapperFormatChecksBothInOneStatement(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	sess, err := store.CreateSession("s", "127.0.0.1", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	prof, err := store.CreateMapperProfile("p", goodSpec)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	profRef := MapperFormatDBRef(prof.ID)

	// Happy path.
	sessionFound, refFound, err := store.AssignSessionMapperFormat(sess.ID, profRef, prof.ID)
	if err != nil || !sessionFound || !refFound {
		t.Fatalf("assign: session=%v ref=%v err=%v", sessionFound, refFound, err)
	}
	if got, _ := store.GetSessionMapperFormat(sess.ID); got != profRef {
		t.Fatalf("assignment not stored: %q", got)
	}

	// A profile that does not exist must be refused AND must not write.
	sessionFound, refFound, err = store.AssignSessionMapperFormat(sess.ID, "db:987654", 987654)
	if err != nil {
		t.Fatalf("assign missing profile: %v", err)
	}
	if !sessionFound {
		t.Error("the session exists — sessionFound should be true")
	}
	if refFound {
		t.Error("a nonexistent profile must be refused")
	}
	if got, _ := store.GetSessionMapperFormat(sess.ID); got != profRef {
		t.Errorf("a refused assign must leave the previous one intact, got %q", got)
	}

	// Nothing protects us at the DB level: a raw write of a dangling reference
	// sticks. 015 dropped the FK (which was never enforced on a pooled connection
	// anyway), so this is why the existence test has to be part of the UPDATE.
	if err := store.DB().Exec(
		`UPDATE sessions SET mapper_format = 'db:987654' WHERE id = ?`, sess.ID).Error; err != nil {
		t.Fatalf("raw dangling write: %v", err)
	}
	if got, _ := store.GetSessionMapperFormat(sess.ID); got != "db:987654" {
		t.Fatalf("expected the dangling reference to persist (nothing at the DB level rejects it), got %q", got)
	}
	if err := store.SetSessionMapperFormat(sess.ID, profRef); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// A BUILTIN reference needs no DB existence check — a compiled-in detector is
	// not a row, and only the in-process registry can say whether it exists. The
	// store accepts it and the web layer is what validates the key.
	sessionFound, refFound, err = store.AssignSessionMapperFormat(sess.ID, "builtin:diku-en", 0)
	if err != nil || !sessionFound || !refFound {
		t.Fatalf("assign builtin: session=%v ref=%v err=%v", sessionFound, refFound, err)
	}
	if got, _ := store.GetSessionMapperFormat(sess.ID); got != "builtin:diku-en" {
		t.Errorf("builtin assignment not stored: %q", got)
	}

	// A nonexistent SESSION is reported separately, so the REST layer can 404 it
	// instead of returning 200 for a write that never happened.
	sessionFound, _, err = store.AssignSessionMapperFormat(424242, profRef, prof.ID)
	if err != nil {
		t.Fatalf("assign to missing session: %v", err)
	}
	if sessionFound {
		t.Error("a nonexistent session must be reported as not found")
	}

	// Clearing needs no existence check.
	sessionFound, refFound, err = store.AssignSessionMapperFormat(sess.ID, "", 0)
	if err != nil || !sessionFound || !refFound {
		t.Fatalf("clear: session=%v ref=%v err=%v", sessionFound, refFound, err)
	}
	if got, _ := store.GetSessionMapperFormat(sess.ID); got != "" {
		t.Errorf("clearing should leave NULL, got %q", got)
	}
}

// TestUpdateMapperProfileRefusesNewerVersion: an older binary must not silently
// downgrade a row a newer one wrote. ParseSpecJSON ignores unknown keys, so the
// save would strip the newer fields AND reset `version` — erasing the only
// marker a future data migration keys on.
func TestUpdateMapperProfileRefusesNewerVersion(t *testing.T) {
	store := newMapperTestStore(t)
	stubMapperValidator(t)

	row, err := store.CreateMapperProfile("fromTheFuture", goodSpec)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.DB().Exec(
		`UPDATE mapper_profiles SET version = ? WHERE id = ?`, MapperSpecVersion+1, row.ID).Error; err != nil {
		t.Fatalf("bump version: %v", err)
	}

	_, found, err := store.UpdateMapperProfile(row.ID, "renamed", goodSpec)
	if !errors.Is(err, ErrMapperProfileNewerVersion) {
		t.Fatalf("update error = %v, want ErrMapperProfileNewerVersion", err)
	}
	if !found {
		t.Error("the row exists — found should be true so the caller reports a conflict, not a 404")
	}
	after, _, _ := store.GetMapperProfile(row.ID)
	if after.Name != "fromTheFuture" || after.Version != MapperSpecVersion+1 {
		t.Errorf("a refused update must change nothing, got %+v", after)
	}
}
