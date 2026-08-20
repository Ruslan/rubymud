package web

import (
	"encoding/json"
	"io"
	"net/http"

	appi18n "rubymud/go/internal/i18n"
	"rubymud/go/internal/session"
)

const mapRecordingBodyLimit = 1024

func decodeEmptyStrict(w http.ResponseWriter, r *http.Request, locale appi18n.Locale) bool {
	r.Body = http.MaxBytesReader(w, r.Body, mapRecordingBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct{}
	if err := dec.Decode(&body); err != nil && err != io.EOF {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidBody, map[string]any{"detail": err.Error()})
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperTrailingData, nil)
		return false
	}
	return true
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any, locale appi18n.Locale) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidBody, map[string]any{"detail": err.Error()})
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperTrailingData, nil)
		return false
	}
	return true
}

func (s *Server) mapReconciliationPreview(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	var in session.MapReconciliationChoice
	if !decodeStrictJSON(w, r, &in, locale) {
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeMapperTextError(w, http.StatusConflict, locale, appi18n.MapperReconciliationDisconnected, nil)
		return
	}
	p, err := sess.PreviewMapReconciliation(in)
	if err != nil {
		writeMapperTextError(w, http.StatusConflict, locale, appi18n.MapperReconciliationFailed, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, p)
}
func (s *Server) mapReconciliationApply(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	var in struct {
		ConflictID uint64 `json:"conflict_id"`
		Token      string `json:"token"`
	}
	if !decodeStrictJSON(w, r, &in, locale) {
		return
	}
	if in.ConflictID == 0 || in.Token == "" {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperReconciliationConflictToken, nil)
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeMapperTextError(w, http.StatusConflict, locale, appi18n.MapperReconciliationDisconnected, nil)
		return
	}
	state, _, err := sess.ApplyMapReconciliation(in.ConflictID, in.Token)
	if err != nil {
		writeMapperTextError(w, http.StatusConflict, locale, appi18n.MapperReconciliationFailed, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"applied": true, "state": state})
}

func (s *Server) mapRecordingState(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	_, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, addMapperReason(map[string]any{"mode": "off", "pending": 0}, locale, appi18n.MapperRecordingSessionNotConnected, nil))
		return
	}
	writeJSON(w, localizeMapRecordingState(locale, sess.MapRecordingState()))
}

func (s *Server) mapRecordingStart(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	if !decodeEmptyStrict(w, r, locale) {
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeMapperTextError(w, http.StatusConflict, locale, appi18n.MapperRecordingSessionNotConnected, nil)
		return
	}
	state, err := sess.StartMapRecording()
	if err != nil {
		out := map[string]any{"started": false, "state": localizeMapRecordingState(locale, state)}
		if key, known := mapperRecordingReasonKey(err.Error()); known {
			addMapperReason(out, locale, key, nil)
		} else {
			out["reason"] = err.Error()
			out["reason_code"] = "mapper.recording.failed"
		}
		writeJSON(w, out)
		return
	}
	sess.BroadcastMapRecordingState()
	writeJSON(w, map[string]any{"started": true, "state": localizeMapRecordingState(locale, state)})
}

func (s *Server) mapRecordingStop(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	if !decodeEmptyStrict(w, r, locale) {
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeMapperTextError(w, http.StatusConflict, locale, appi18n.MapperRecordingSessionNotConnected, nil)
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
		writeJSON(w, addMapperReason(map[string]any{"stopped": false, "state": localizeMapRecordingState(locale, state)}, locale, appi18n.MapperRecordingReconcileInProgress, nil))
		return
	}
	sess.BroadcastMapRecordingState()
	writeJSON(w, map[string]any{"stopped": true, "state": localizeMapRecordingState(locale, state)})
}
