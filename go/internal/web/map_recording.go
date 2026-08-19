package web

import (
	"encoding/json"
	"io"
	"net/http"

	"rubymud/go/internal/session"
)

const mapRecordingBodyLimit = 1024

func decodeEmptyStrict(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, mapRecordingBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct{}
	if err := dec.Decode(&body); err != nil && err != io.EOF {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "trailing data", http.StatusBadRequest)
		return false
	}
	return true
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		http.Error(w, "invalid body: "+err.Error(), 400)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "trailing data", 400)
		return false
	}
	return true
}

func (s *Server) mapReconciliationPreview(w http.ResponseWriter, r *http.Request) {
	var in session.MapReconciliationChoice
	if !decodeStrictJSON(w, r, &in) {
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", 400)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		http.Error(w, "session not connected", 409)
		return
	}
	p, err := sess.PreviewMapReconciliation(in)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, p)
}
func (s *Server) mapReconciliationApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConflictID uint64 `json:"conflict_id"`
		Token      string `json:"token"`
	}
	if !decodeStrictJSON(w, r, &in) {
		return
	}
	if in.ConflictID == 0 || in.Token == "" {
		http.Error(w, "conflict_id and token are required", 400)
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", 400)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		http.Error(w, "session not connected", 409)
		return
	}
	state, setID, err := sess.ApplyMapReconciliation(in.ConflictID, in.Token)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	s.manager.RefreshSharedTopology(setID, id)
	writeJSON(w, map[string]any{"applied": true, "state": state})
}

func (s *Server) mapRecordingState(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", 400)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"mode": "off", "reason": "session not connected", "pending": 0})
		return
	}
	writeJSON(w, sess.MapRecordingState())
}

func (s *Server) mapRecordingStart(w http.ResponseWriter, r *http.Request) {
	if !decodeEmptyStrict(w, r) {
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", 400)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		http.Error(w, "session not connected", 409)
		return
	}
	state, err := sess.StartMapRecording()
	if err != nil {
		writeJSON(w, map[string]any{"started": false, "reason": err.Error(), "state": state})
		return
	}
	sess.BroadcastMapRecordingState()
	writeJSON(w, map[string]any{"started": true, "state": state})
}

func (s *Server) mapRecordingStop(w http.ResponseWriter, r *http.Request) {
	if !decodeEmptyStrict(w, r) {
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", 400)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		http.Error(w, "session not connected", 409)
		return
	}
	var state session.MapRecordingState
	var stopped bool
	if s.mapRecordingTryStop != nil {
		state, stopped = s.mapRecordingTryStop(sess)
	} else {
		state, stopped = sess.TryStopMapRecording("stopped by user")
	}
	if !stopped {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]any{"stopped": false, "reason": "reconciliation apply is in progress", "state": state})
		return
	}
	sess.BroadcastMapRecordingState()
	writeJSON(w, map[string]any{"stopped": true, "state": state})
}
