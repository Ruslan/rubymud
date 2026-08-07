package storage

import (
	"testing"
)

func bPtr(v bool) *bool { return &v }

// TestUpsertRoomAnnotationCreateThenEdit covers the core edit-in-place contract:
// a first upsert creates, a second upsert on the same coord updates in place
// (updated_at advances), and a partial update preserves fields it does not touch.
func TestUpsertRoomAnnotationCreateThenEdit(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	// Create.
	a1, err := store.UpsertRoomAnnotation(id, "Alpha", 0, 0, 0, AnnotationFields{
		Note:   ptrStr("watch the pit"),
		Hazard: ptrStr("spikes"),
		Author: ptrStr("ru"),
	})
	if err != nil {
		t.Fatalf("upsert create: %v", err)
	}
	if a1.ID == 0 {
		t.Fatal("expected a persisted id")
	}
	if a1.Note != "watch the pit" || a1.Hazard != "spikes" || a1.Author != "ru" {
		t.Fatalf("create fields: %+v", a1)
	}
	if a1.DT { // untouched bool defaults to false
		t.Fatalf("dt should default false: %+v", a1)
	}
	if a1.UpdatedAt == nil || a1.UpdatedAt.Time.IsZero() {
		t.Fatalf("updated_at not set on create: %+v", a1)
	}

	// Partial edit-in-place: only note changes; hazard/author preserved.
	a2, err := store.UpsertRoomAnnotation(id, "Alpha", 0, 0, 0, AnnotationFields{
		Note: ptrStr("pit is deeper now"),
	})
	if err != nil {
		t.Fatalf("upsert edit: %v", err)
	}
	if a2.ID != a1.ID {
		t.Fatalf("edit-in-place must reuse the same row: %d vs %d", a2.ID, a1.ID)
	}
	if a2.Note != "pit is deeper now" {
		t.Fatalf("note not updated: %+v", a2)
	}
	if a2.Hazard != "spikes" || a2.Author != "ru" {
		t.Fatalf("partial update clobbered preserved fields: %+v", a2)
	}
	if !a2.UpdatedAt.Time.After(a1.UpdatedAt.Time) && !a2.UpdatedAt.Time.Equal(a1.UpdatedAt.Time) {
		// updated_at should not go backwards; typically strictly after.
		t.Fatalf("updated_at regressed: %v -> %v", a1.UpdatedAt.Time, a2.UpdatedAt.Time)
	}

	// Explicit clear: empty string clears note; false pointer leaves dt false.
	a3, err := store.UpsertRoomAnnotation(id, "Alpha", 0, 0, 0, AnnotationFields{
		Note: ptrStr(""),
		DT:   bPtr(true),
	})
	if err != nil {
		t.Fatalf("upsert clear: %v", err)
	}
	if a3.Note != "" {
		t.Fatalf("empty-string note should clear: %+v", a3)
	}
	if !a3.DT {
		t.Fatalf("dt should be set true: %+v", a3)
	}
	if a3.Hazard != "spikes" {
		t.Fatalf("clear should not touch hazard: %+v", a3)
	}
}

func TestGetAndListRoomAnnotations(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	// Missing annotation is not an error.
	if _, ok, err := store.GetRoomAnnotation(id, "Alpha", 9, 9, 9); err != nil || ok {
		t.Fatalf("missing get: ok=%v err=%v", ok, err)
	}

	if _, err := store.UpsertRoomAnnotation(id, "Alpha", 0, 0, 0, AnnotationFields{Note: ptrStr("a")}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.UpsertRoomAnnotation(id, "Beta", 0, 0, 1, AnnotationFields{DT: bPtr(true)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := store.GetRoomAnnotation(id, "Alpha", 0, 0, 0)
	if err != nil || !ok {
		t.Fatalf("get present: ok=%v err=%v", ok, err)
	}
	if got.Note != "a" {
		t.Fatalf("get body: %+v", got)
	}

	all, err := store.ListRoomAnnotations(id, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 annotations, got %d", len(all))
	}

	beta, err := store.ListRoomAnnotations(id, "Beta")
	if err != nil {
		t.Fatalf("list beta: %v", err)
	}
	if len(beta) != 1 || beta[0].Zone != "Beta" || !beta[0].DT {
		t.Fatalf("zone filter: %+v", beta)
	}
}
