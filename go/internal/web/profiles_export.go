package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"rubymud/go/internal/storage"
)

func (s *Server) exportProfileToFile(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	filename, err := s.doExportProfile(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"filename": filename})
}

func (s *Server) exportAllProfilesToFiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var filenames []string
	for _, p := range profiles {
		fn, err := s.doExportProfile(p.ID)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to export %s: %v", p.Name, err), http.StatusInternalServerError)
			return
		}
		filenames = append(filenames, fn)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filenames)
}

func (s *Server) doExportProfile(pid int64) (string, error) {
	p, err := s.store.GetProfile(pid)
	if err != nil {
		return "", err
	}

	content, err := s.store.ExportProfileScript(pid)
	if err != nil {
		return "", err
	}

	filename := storage.SanitizeFilename(p.Name) + ".tt"
	path := filepath.Join(s.configDir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return filename, nil
}

func (s *Server) listProfileFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.configDir)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type FileEntry struct {
		Filename string `json:"filename"`
	}
	var files []FileEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tt") {
			files = append(files, FileEntry{Filename: entry.Name()})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (s *Server) importProfileFromFile(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Filename string `json:"filename"`
		SessionID int64 `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filename := filepath.Base(payload.Filename) // Prevent path traversal
	if filename == "" || filename == "." || filename == "/" {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	if _, err := s.doImportProfile(filename, payload.SessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) importAllProfilesFromFiles(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SessionID int64 `json:"session_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}

	entries, err := os.ReadDir(s.configDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tt") {
			if _, err := s.doImportProfile(entry.Name(), payload.SessionID); err != nil {
				http.Error(w, fmt.Sprintf("Failed to import %s: %v", entry.Name(), err), http.StatusInternalServerError)
				return
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) doImportProfile(filename string, sessionID int64) (storage.Profile, error) {
	path := filepath.Join(s.configDir, filename)
	content, err := os.ReadFile(path)
	if err != nil {
		return storage.Profile{}, err
	}

	ps, err := storage.ParseProfileScript(string(content))
	if err != nil {
		return storage.Profile{}, err
	}
	if ps.Name == "" {
		ps.Name = strings.TrimSuffix(filename, ".tt")
	}

	p, err := s.store.ImportProfileScript(ps)
	if err != nil {
		return storage.Profile{}, err
	}
	if sessionID != 0 {
		if err := s.store.EnsureProfileInSession(sessionID, p.ID); err != nil {
			return storage.Profile{}, err
		}
	}
	s.notifyProfileChanged(p.ID, "profiles")
	return p, nil
}
