package storage

import (
	"database/sql"
	"errors"
)

func (s *Store) EnsureDefaultSession(host string, port int) (SessionRecord, error) {
	const query = `
		SELECT id, name, mud_host, mud_port, status
		FROM sessions
		WHERE name = 'default'
		ORDER BY id ASC
		LIMIT 1
	`

	var record SessionRecord
	err := s.db.QueryRow(query).Scan(&record.ID, &record.Name, &record.MudHost, &record.MudPort, &record.Status)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := s.db.Exec(`
			INSERT INTO sessions(name, mud_host, mud_port, status)
			VALUES('default', ?, ?, 'connected')
		`, host, port)
		if insertErr != nil {
			return SessionRecord{}, insertErr
		}

		id, idErr := result.LastInsertId()
		if idErr != nil {
			return SessionRecord{}, idErr
		}

		return SessionRecord{ID: id, Name: "default", MudHost: host, MudPort: port, Status: "connected"}, nil
	}
	if err != nil {
		return SessionRecord{}, err
	}

	_, err = s.db.Exec(`
		UPDATE sessions
		SET mud_host = ?, mud_port = ?, status = 'connected', updated_at = CURRENT_TIMESTAMP, last_connected_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, host, port, record.ID)
	if err != nil {
		return SessionRecord{}, err
	}

	record.MudHost = host
	record.MudPort = port
	record.Status = "connected"
	return record, nil
}

func (s *Store) MarkSessionDisconnected(sessionID int64) error {
	_, err := s.db.Exec(`
		UPDATE sessions
		SET status = 'disconnected', updated_at = CURRENT_TIMESTAMP, last_disconnected_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, sessionID)
	return err
}
