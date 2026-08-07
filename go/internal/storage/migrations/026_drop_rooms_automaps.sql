-- map_edges has been authoritative since 023. The Go preflight in
-- runMigrations audits every raw legacy occurrence in this same transaction
-- before this supported SQLite schema operation removes the retired source.
ALTER TABLE rooms DROP COLUMN automaps;
