package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	appi18n "rubymud/go/internal/i18n"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// ---------------------------------------------------------------------------
// Room-format profiles ("mapper profiles") — REST.
//
// These are ROOM TEXT FORMATS, not the TinTin alias/trigger `profiles` served by
// /api/profiles. The routes are /api/mapper-profiles and
// /api/sessions/{id}/mapper-profile; nothing here touches the other entity.
//
// Conventions follow the neighbouring mapper endpoints in mapper.go: writeJSON
// for success, http.Error with a plain message the UI shows verbatim for a bad
// request, and soft-fail 200s where a stale UI tab is a normal occurrence.
//
// AGENTS #2 (admin/game sync) is the whole point of the write paths here: every
// mutation that can change what a LIVE session parses with ends by calling
// Session.LoadMapperProfile on the affected sessions, so a settings edit reaches
// the running session with no reconnect.
// ---------------------------------------------------------------------------

// builtinMapperProfileID is the id every synthetic builtin entry carries in the
// list response and accepts nowhere else. 0 rather than null so the UI's row
// keying can use a plain number and "0 == not a row" reads the same in Go and TS.
// Builtins are distinguished from each other by `ref`, never by id.
const builtinMapperProfileID = 0

const (
	mapperReplayDefaultLines = 1000
	mapperReplayMaxLines     = 5000
	mapperReplaySampleLimit  = 20
)

// mapperProfileJSON is the wire shape of one selectable room format — a
// registered detector OR a stored row. The selector and the list render the same
// shape for both, which is what stops a Go detector
// from needing a parallel UI.
//
// Spec is the raw ProfileSpec JSON (not a string) so the settings form binds to
// real fields instead of parsing a string a second time. It is NULL for a Go
// detector — there is no spec — and `editable` is what the UI keys off.
type mapperProfileJSON struct {
	// Ref is the value written to sessions.mapper_format ("builtin:rmud", "db:7").
	// It is the identity of an entry; `id` is a back-compat convenience.
	Ref     string          `json:"ref"`
	ID      int64           `json:"id"`
	Name    string          `json:"name"`
	Builtin bool            `json:"builtin"`
	Version int             `json:"version"`
	Spec    json.RawMessage `json:"spec,omitempty"`

	// The DetectorInfo fields, flattened so the UI does not have to reach through
	// a nested object for the two strings it renders on every row.
	Title       string `json:"title"`
	MUD         string `json:"mud"`
	Provenance  string `json:"provenance"`
	Description string `json:"description"`
	Editable    bool   `json:"editable"`
	// GoDetector marks a contributed Go detector: read-only, no spec, and it
	// cannot be copied into an editable format. `editable` already implies it for
	// the builtins, but a db row is editable and NOT a Go detector, so the UI needs
	// both bits to render the right note.
	GoDetector bool `json:"go_detector"`
}

// registeredDetectorEntries renders every compiled-in detector as a list entry.
//
// A builtin is NOT a row (see migration 014): keeping them out of the table is
// what makes them undeletable and un-corruptible, and this is how the UI still
// gets to display them and offer «make a copy» for the declarative ones.
//
// The spec is included only for a declarative detector, obtained through
// mapper.SpecProvider. A Go detector does not implement it, which is the same
// fact its DetectorInfo.Editable=false states — so the two cannot drift.
// The ORDER is deliberate: the default detector first, then the rest by key
// (mapper.DetectorInfos is already key-sorted, which would otherwise put
// "diku-en" ahead of "rmud" purely alphabetically). The default is what almost
// every user wants and what NULL resolves to, so it leads the selector.
func registeredDetectorEntries() []mapperProfileJSON {
	infos := mapper.DetectorInfos()
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].Key == mapper.DefaultDetectorKey && infos[j].Key != mapper.DefaultDetectorKey
	})
	out := make([]mapperProfileJSON, 0, len(infos))
	for _, info := range infos {
		entry := mapperProfileJSON{
			Ref:         mapper.BuiltinRef(info.Key),
			ID:          builtinMapperProfileID,
			Name:        info.Key,
			Builtin:     true,
			Version:     storage.MapperSpecVersion,
			Title:       info.Title,
			MUD:         info.MUD,
			Provenance:  string(info.Provenance),
			Description: info.Description,
			Editable:    info.Editable,
			GoDetector:  !info.Editable,
		}
		if det, ok := mapper.NewDetector(info.Key); ok {
			if sp, ok := det.(mapper.SpecProvider); ok {
				if raw, err := mapper.MarshalSpec(sp.Spec()); err == nil {
					entry.Spec = raw
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

func mapperProfileRowJSON(row storage.MapperProfile) mapperProfileJSON {
	return mapperProfileJSON{
		Ref:         mapper.FormatRefForDB(row.ID),
		ID:          row.ID,
		Name:        row.Name,
		Builtin:     false,
		Version:     row.Version,
		Spec:        json.RawMessage(row.Spec),
		Title:       row.Name,
		Provenance:  string(mapper.ProvenanceUser),
		Description: "A room format written here, in this installation's settings.",
		Editable:    true,
	}
}

// listMapperProfiles handles GET /api/mapper-profiles. The compiled-in detectors
// come first (ordered by key), then the stored rows.
func (s *Server) listMapperProfiles(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	rows, err := s.store.ListMapperProfiles()
	if err != nil {
		writeMapperProfileError(w, http.StatusInternalServerError, locale, err)
		return
	}
	builtins := registeredDetectorEntries()
	out := make([]mapperProfileJSON, 0, len(rows)+len(builtins))
	out = append(out, builtins...)
	for _, row := range rows {
		out = append(out, mapperProfileRowJSON(row))
	}
	writeJSON(w, out)
}

// mapperProfileBody is the create/update/validate request body. Spec is raw JSON
// so it survives the trip unaltered up to the point it is normalized.
type mapperProfileBody struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

// normalizeMapperSpec is the single write-path gate: it parses the submitted
// spec, forces spec.name to the row name (so Profile.Name() and the row can
// never drift into two different answers to "which format is this?"), compiles
// it, and re-marshals the COMPILED spec.
//
// Re-marshalling matters: what lands in the DB is then exactly the shape the
// compiler accepted, with any unknown or junk keys from the request body
// dropped, so the stored bytes and the running rules cannot disagree.
func normalizeMapperSpec(name string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("missing spec")
	}
	spec, err := mapper.ParseSpecJSON(raw)
	if err != nil {
		return "", err
	}
	spec.Name = name
	prof, err := mapper.Compile(spec)
	if err != nil {
		return "", err
	}
	out, err := mapper.MarshalSpec(prof.Spec())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// createMapperProfile handles POST /api/mapper-profiles.
func (s *Server) createMapperProfile(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	var body mapperProfileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidBody, map[string]any{"detail": err.Error()})
		return
	}
	name := strings.TrimSpace(body.Name)
	specJSON, err := normalizeMapperSpec(name, body.Spec)
	if err != nil {
		key := appi18n.MapperProfileValidationFailed
		if err.Error() == "missing spec" {
			key = appi18n.MapperProfileMissingSpec
		}
		writeMapperTextError(w, http.StatusBadRequest, locale, key, map[string]any{"detail": err.Error()})
		return
	}
	row, err := s.store.CreateMapperProfile(name, specJSON)
	if err != nil {
		writeMapperProfileError(w, mapperProfileWriteStatus(err), locale, err)
		return
	}
	writeJSON(w, mapperProfileRowJSON(row))
}

// updateMapperProfile handles PUT /api/mapper-profiles/{id}. After the write it
// re-applies the profile to every LIVE session assigned to it — editing the
// format a session is currently parsing with must take effect immediately
// (AGENTS #2), without disturbing that session's tracker.
func (s *Server) updateMapperProfile(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	id, err := mapperProfileIDParam(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileInvalidID, nil)
		return
	}
	if rejectBuiltinMapperProfile(w, id, locale) {
		return
	}
	var body mapperProfileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidBody, map[string]any{"detail": err.Error()})
		return
	}
	name := strings.TrimSpace(body.Name)
	specJSON, err := normalizeMapperSpec(name, body.Spec)
	if err != nil {
		key := appi18n.MapperProfileValidationFailed
		if err.Error() == "missing spec" {
			key = appi18n.MapperProfileMissingSpec
		}
		writeMapperTextError(w, http.StatusBadRequest, locale, key, map[string]any{"detail": err.Error()})
		return
	}
	row, found, err := s.store.UpdateMapperProfile(id, name, specJSON)
	if err != nil {
		writeMapperProfileError(w, mapperProfileWriteStatus(err), locale, err)
		return
	}
	if !found {
		writeMapperTextError(w, http.StatusNotFound, locale, appi18n.MapperProfileNotFound, nil)
		return
	}
	s.reapplyMapperProfile(id)
	writeJSON(w, mapperProfileRowJSON(row))
}

// deleteMapperProfile handles DELETE /api/mapper-profiles/{id}. The store clears
// the assignment off every referencing session in the same transaction, and
// reports which ones it cleared, so the re-apply afterwards resolves exactly
// those sessions to the builtin.
func (s *Server) deleteMapperProfile(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	id, err := mapperProfileIDParam(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileInvalidID, nil)
		return
	}
	if rejectBuiltinMapperProfile(w, id, locale) {
		return
	}
	// The affected session ids come back FROM the delete transaction — see
	// Store.DeleteMapperProfile. Reading them beforehand would miss a session
	// assigned in the window between the two statements.
	found, affected, err := s.store.DeleteMapperProfile(id)
	if err != nil {
		writeMapperProfileError(w, http.StatusInternalServerError, locale, err)
		return
	}
	if !found {
		writeMapperTextError(w, http.StatusNotFound, locale, appi18n.MapperProfileNotFound, nil)
		return
	}
	for _, sid := range affected {
		if sess, ok := s.manager.GetSession(sid); ok {
			sess.LoadMapperProfile()
		}
	}
	writeJSON(w, map[string]any{"deleted": true, "id": id, "sessions_reset": len(affected)})
}

// validateMapperProfileSpec handles POST /api/mapper-profiles/validate — a
// compile-only check with no write, returning {ok, error} with mapper.Compile's
// verbatim message. This is what the settings form calls for inline feedback, so
// the BACKEND stays the single source of validation truth and the TS side never
// reimplements a rule.
func (s *Server) validateMapperProfileSpec(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	var body mapperProfileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, map[string]any{
			"ok": false, "error": renderMapperWireText(locale, string(appi18n.MapperInvalidBody), map[string]any{"detail": err.Error()}),
			"error_code": string(appi18n.MapperInvalidBody), "error_args": map[string]any{"detail": err.Error()}, "error_detail": err.Error(),
		})
		return
	}
	if _, err := normalizeMapperSpec(strings.TrimSpace(body.Name), body.Spec); err != nil {
		key := appi18n.MapperProfileValidationFailed
		if err.Error() == "missing spec" {
			key = appi18n.MapperProfileMissingSpec
		}
		writeMapperProfileValidationJSON(w, http.StatusOK, locale, key, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "error": ""})
}

type mapperProfileReplayBody struct {
	mapperProfileBody
	SessionID int64 `json:"session_id"`
	Limit     int   `json:"limit"`
}

// replayMapperProfileSpec compiles an unsaved draft and runs it over recent
// canonical MUD output. It is an explicit admin action and performs no writes;
// in particular it never touches the live detector or mapper tracker.
func (s *Server) replayMapperProfileSpec(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	var body mapperProfileReplayBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidBody, map[string]any{"detail": err.Error()})
		return
	}
	if body.SessionID <= 0 {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileSessionNotFound, nil)
		return
	}
	if _, err := s.store.GetSession(body.SessionID); err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileSessionNotFound, nil)
		return
	}
	if body.Limit == 0 {
		body.Limit = mapperReplayDefaultLines
	}
	if body.Limit < 1 || body.Limit > mapperReplayMaxLines {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileReplayLimit, map[string]any{"max": mapperReplayMaxLines})
		return
	}
	if len(body.Spec) == 0 {
		writeMapperProfileValidationJSON(w, http.StatusBadRequest, locale, appi18n.MapperProfileMissingSpec, "missing spec")
		return
	}
	spec, err := mapper.ParseSpecJSON(body.Spec)
	if err != nil {
		writeMapperProfileValidationJSON(w, http.StatusBadRequest, locale, appi18n.MapperProfileValidationFailed, err.Error())
		return
	}
	spec.Name = strings.TrimSpace(body.Name)
	profile, err := mapper.Compile(spec)
	if err != nil {
		writeMapperProfileValidationJSON(w, http.StatusBadRequest, locale, appi18n.MapperProfileValidationFailed, err.Error())
		return
	}
	entries, err := s.store.RecentMudLogs(body.SessionID, body.Limit)
	if err != nil {
		writeMapperProfileError(w, http.StatusInternalServerError, locale, err)
		return
	}
	lines := make([]mapper.ReplayLine, len(entries))
	for i, entry := range entries {
		lines[i] = mapper.ReplayLine{ID: entry.ID, Plain: entry.PlainText, Raw: entry.RawText}
	}
	result := mapper.ReplayProfile(profile, lines, mapperReplaySampleLimit)
	writeJSON(w, struct {
		OK        bool  `json:"ok"`
		SessionID int64 `json:"session_id"`
		mapper.ReplayResult
	}{OK: true, SessionID: body.SessionID, ReplayResult: result})
}

// getSessionMapperProfile handles GET /api/sessions/{sessionID}/mapper-profile.
// It reports both the STORED assignment and, when the session is live, what it
// actually ended up parsing with — those differ exactly when a stored profile
// failed to load, which is the case the user needs to see.
func (s *Server) getSessionMapperProfile(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	sess, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	stored, err := s.store.GetSessionMapperFormat(id)
	if err != nil {
		writeMapperProfileError(w, http.StatusInternalServerError, locale, err)
		return
	}
	out := map[string]any{
		// mapper_format is the STORED reference; "" means NULL = the default.
		"mapper_format": stored,
		// mapper_profile_id is kept for the row-highlighting the settings list
		// already does: the row id when the stored reference is a db: one, else null.
		"mapper_profile_id": nil,
		"live":              false,
		"active_ref":        "",
		"active_id":         nil,
		"active_name":       "",
		"error":             "",
	}
	if kind, _, rowID, ok := mapper.ParseFormatRef(stored); ok && kind == mapper.RefDB {
		out["mapper_profile_id"] = rowID
	}
	if sess != nil {
		activeRef, activeName, warn := sess.MapperProfileStatus()
		out["live"] = true
		out["active_ref"] = activeRef
		out["active_id"] = sess.MapperProfileRowID()
		out["active_name"] = activeName
		addMapperProfileWarning(out, locale, warn)
	}
	writeJSON(w, out)
}

// setSessionMapperProfile handles POST /api/sessions/{sessionID}/mapper-profile.
//
// The body is {"format": "<ref>"} where ref is "" (the default), "builtin:<key>"
// or "db:<id>". {"profile_id": N|null} is ALSO accepted for compatibility with
// older settings tabs: an already-open tab can still send it after an upgrade, and
// rejecting it would turn a deploy into a silent failure for anyone who did not
// reload. `format` wins when both are present.
//
// It persists the column and then re-applies to the live session, which rebuilds
// ONLY the detector — the tracker's position, confidence, pending queue and index
// are untouched.
func (s *Server) setSessionMapperProfile(w http.ResponseWriter, r *http.Request) {
	locale := s.mapperLanguage()
	sess, id, err := s.getSession(r)
	if err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidSessionID, nil)
		return
	}
	var body struct {
		Format *string `json:"format"`
		// Legacy row-id form. See the doc comment.
		ProfileID *int64 `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperInvalidBody, map[string]any{"detail": err.Error()})
		return
	}

	ref := ""
	switch {
	case body.Format != nil:
		ref = strings.TrimSpace(*body.Format)
	case body.ProfileID != nil && *body.ProfileID > 0:
		ref = mapper.FormatRefForDB(*body.ProfileID)
	}

	kind, key, rowID, ok := mapper.ParseFormatRef(ref)
	if !ok {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileUnknownRef, map[string]any{"ref": ref})
		return
	}
	// CANONICALIZE before storing. ParseFormatRef is deliberately lenient (it
	// accepts "db:007" and "builtin: rmud "), but `storage` matches this column by
	// EXACT STRING: DeleteMapperProfile clears sessions WHERE mapper_format = 'db:7'
	// and SessionIDsUsingMapperProfile finds the live sessions to re-apply the same
	// way. Storing the raw input would mean a session referencing "db:007" is found
	// by neither — editing that profile would never reach the live session, and
	// deleting it would leave the session pointing at a row that no longer exists,
	// with nothing reporting either. Re-rendering here is the whole fix; the empty
	// string is the default and is stored as SQL NULL.
	ref = mapper.CanonicalRef(kind, key, rowID)
	if kind == mapper.RefBuiltin {
		// A builtin's existence is only knowable in-process: the registry, not the DB.
		// Checking it HERE rather than in the store is what keeps the store free of
		// any knowledge of the detector registry.
		if _, found := mapper.LookupDetectorInfo(key); !found {
			writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileNotCompiled, map[string]any{"ref": ref})
			return
		}
	}

	// For a db: reference the existence check and the write happen in ONE statement
	// inside the store — a separate check would let a concurrent DELETE leave the
	// column pointing at a vanished row forever (migration 015 dropped the FK, and
	// the FK never caught it anyway; see the store method).
	sessionFound, refFound, err := s.store.AssignSessionMapperFormat(id, ref, rowID)
	if err != nil {
		writeMapperProfileError(w, http.StatusInternalServerError, locale, err)
		return
	}
	if !sessionFound {
		writeMapperTextError(w, http.StatusNotFound, locale, appi18n.MapperAdminSessionNotFound, map[string]any{"id": id})
		return
	}
	if !refFound {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileNotFound, nil)
		return
	}

	out := map[string]any{
		"mapper_format":     ref,
		"mapper_profile_id": nil,
		"live":              false,
		"active_ref":        "",
		"active_name":       "",
		"error":             "",
	}
	if kind == mapper.RefDB {
		out["mapper_profile_id"] = rowID
	}
	if sess != nil {
		sess.LoadMapperProfile()
		activeRef, activeName, warn := sess.MapperProfileStatus()
		out["live"] = true
		out["active_ref"] = activeRef
		out["active_name"] = activeName
		addMapperProfileWarning(out, locale, warn)
	}
	writeJSON(w, out)
}

// reapplyMapperProfile pushes a live rebuild to every connected session assigned
// to the given profile id. Sessions that are not connected pick the change up
// when they next connect (Manager.Connect calls LoadMapperProfile).
func (s *Server) reapplyMapperProfile(id int64) {
	ids, err := s.store.SessionIDsUsingMapperProfile(id)
	if err != nil {
		return
	}
	for _, sid := range ids {
		if sess, ok := s.manager.GetSession(sid); ok {
			sess.LoadMapperProfile()
		}
	}
}

func mapperProfileIDParam(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// rejectBuiltinMapperProfile blocks a write addressed at the synthetic builtin
// entry. The builtin is not a row, so id 0 could never match one and the write
// would 404 anyway — but saying so explicitly states the INTENT (the builtin is
// read-only by construction) instead of leaning on AUTOINCREMENT never issuing 0.
func rejectBuiltinMapperProfile(w http.ResponseWriter, id int64, locale appi18n.Locale) bool {
	if id == builtinMapperProfileID {
		writeMapperTextError(w, http.StatusBadRequest, locale, appi18n.MapperProfileBuiltinReadOnly, nil)
		return true
	}
	return false
}

// mapperProfileWriteStatus separates "the user typed something invalid" (400,
// the form shows it inline) from "the DB broke" (500).
func mapperProfileWriteStatus(err error) int {
	switch {
	case errors.Is(err, storage.ErrMapperProfileNameRequired),
		errors.Is(err, storage.ErrMapperProfileNameTaken):
		return http.StatusBadRequest
	case errors.Is(err, storage.ErrMapperProfileNewerVersion):
		// Not the user's mistake and not a server fault: the row belongs to a newer
		// build. 409 so the UI can say "refresh/upgrade", not "you typed it wrong".
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeMapperProfileError(w http.ResponseWriter, status int, locale appi18n.Locale, err error) {
	key := appi18n.MapperProfileOperationFailed
	args := map[string]any{"detail": err.Error()}
	switch {
	case errors.Is(err, storage.ErrMapperProfileNameRequired):
		key, args = appi18n.MapperProfileNameRequired, nil
	case errors.Is(err, storage.ErrMapperProfileNameTaken):
		key, args = appi18n.MapperProfileNameTaken, nil
	}
	writeMapperTextError(w, status, locale, key, args)
}
