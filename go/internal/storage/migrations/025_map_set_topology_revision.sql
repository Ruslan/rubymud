-- Optimistic concurrency token for mutable map topology. It is copied on COW.
ALTER TABLE map_sets ADD COLUMN topology_revision INTEGER NOT NULL DEFAULT 0;
