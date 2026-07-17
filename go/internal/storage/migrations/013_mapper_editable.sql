-- Mapper write-mode — editable (writable) map sets (Phase 5, slice 2).
-- An imported-from-archive set is the user's frozen source of truth (editable=0).
-- The first topology write to a frozen set forks it copy-on-write into an
-- editable copy (editable=1, forked_from_id = the source set's id); writes then
-- land on the fork. Annotations are a separate overlay and never fork, so they
-- do not need these columns.
ALTER TABLE map_sets ADD COLUMN editable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE map_sets ADD COLUMN forked_from_id INTEGER;
