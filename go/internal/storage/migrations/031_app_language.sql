CREATE TABLE IF NOT EXISTS app_settings (
  "key" TEXT PRIMARY KEY,
  "value" TEXT
);

INSERT OR IGNORE INTO app_settings ("key", "value") VALUES ('language', 'en');
