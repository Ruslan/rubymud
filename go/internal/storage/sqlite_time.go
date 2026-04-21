package storage

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

const sqliteTimeFormat = time.RFC3339Nano

type SQLiteTime struct {
	time.Time
}

func nowSQLiteTime() SQLiteTime {
	return SQLiteTime{Time: time.Now().UTC()}
}

func nowSQLiteTimePtr() *SQLiteTime {
	t := nowSQLiteTime()
	return &t
}

func parseSQLiteTime(value string) (time.Time, error) {
	formats := []string{
		sqliteTimeFormat,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}

	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("parse sqlite time %q: unsupported format", value)
}

func (t SQLiteTime) Value() (driver.Value, error) {
	if t.Time.IsZero() {
		return nil, nil
	}
	return t.Time.UTC().Format(sqliteTimeFormat), nil
}

func (t *SQLiteTime) Scan(value any) error {
	if value == nil {
		t.Time = time.Time{}
		return nil
	}

	switch v := value.(type) {
	case time.Time:
		t.Time = v.UTC()
		return nil
	case string:
		parsed, err := parseSQLiteTime(v)
		if err != nil {
			return err
		}
		t.Time = parsed
		return nil
	case []byte:
		parsed, err := parseSQLiteTime(string(v))
		if err != nil {
			return err
		}
		t.Time = parsed
		return nil
	default:
		return fmt.Errorf("scan sqlite time from %T", value)
	}
}

func (t SQLiteTime) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Time.UTC().Format(sqliteTimeFormat))
}

func (t *SQLiteTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		t.Time = time.Time{}
		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	parsed, err := parseSQLiteTime(value)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}
