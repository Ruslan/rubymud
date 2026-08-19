package storage

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAppLanguageMigrationPersistsEnglishDefaultWithoutOverwriting(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("MigrateForTest: %v", err)
	}

	var language string
	if err := db.Raw(`SELECT value FROM app_settings WHERE "key" = 'language'`).Scan(&language).Error; err != nil {
		t.Fatalf("read default language: %v", err)
	}
	if language != "en" {
		t.Fatalf("default language = %q, want en", language)
	}

	if err := db.Exec(`UPDATE app_settings SET value = 'ru' WHERE "key" = 'language'`).Error; err != nil {
		t.Fatalf("set ru language: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = '031_app_language.sql'`).Error; err != nil {
		t.Fatalf("rewind language migration: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("reapply language migration: %v", err)
	}
	language = ""
	if err := db.Raw(`SELECT value FROM app_settings WHERE "key" = 'language'`).Scan(&language).Error; err != nil {
		t.Fatalf("read preserved language: %v", err)
	}
	if language != "ru" {
		t.Fatalf("language after migration replay = %q, want preserved ru", language)
	}
}
