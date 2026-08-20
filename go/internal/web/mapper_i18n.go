package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	appi18n "rubymud/go/internal/i18n"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
)

func writeMapperJSONError(w http.ResponseWriter, status int, locale appi18n.Locale, key appi18n.Key, args map[string]any, extra map[string]any) {
	out := extra
	if out == nil {
		out = make(map[string]any)
	}
	out["error"] = renderMapperWireText(locale, string(key), args)
	out["error_code"] = string(key)
	if _, ok := out["conflicts"]; !ok {
		out["conflicts"] = []any{}
	}
	if _, ok := out["conflicts_truncated"]; !ok {
		out["conflicts_truncated"] = false
	}
	if len(args) > 0 {
		out["error_args"] = args
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, out)
}

func writeMapperTextError(w http.ResponseWriter, status int, locale appi18n.Locale, key appi18n.Key, args map[string]any) {
	w.Header().Set("X-RubyMUD-Reason-Code", string(key))
	if len(args) > 0 {
		if raw, err := json.Marshal(args); err == nil {
			w.Header().Set("X-RubyMUD-Reason-Args", string(raw))
		}
	}
	http.Error(w, renderMapperWireText(locale, string(key), args), status)
}

func writeMapperProfileValidationJSON(w http.ResponseWriter, status int, locale appi18n.Locale, key appi18n.Key, detail string) {
	args := map[string]any{"detail": detail}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"ok": false, "error": renderMapperWireText(locale, string(key), args),
		"error_code": string(key), "error_args": args, "error_detail": detail,
	})
}

// addMapperReason keeps the historical human-readable reason while adding a
// stable machine key and typed arguments. Only RubyMUD-authored text is passed
// through the catalog; room names, MUD commands and parser direction tokens are
// opaque argument values.
func addMapperReason(out map[string]any, locale appi18n.Locale, key appi18n.Key, args map[string]any) map[string]any {
	out["reason"] = renderMapperWireText(locale, string(key), args)
	out["reason_code"] = string(key)
	if len(args) > 0 {
		out["reason_args"] = args
	}
	return out
}

func mapperReasonBody(locale appi18n.Locale, ok bool, key appi18n.Key, args map[string]any) map[string]any {
	return addMapperReason(map[string]any{"ok": ok}, locale, key, args)
}

func recordRefusalLocale(locale appi18n.Locale, key appi18n.Key, args map[string]any) map[string]any {
	return addMapperReason(map[string]any{
		"applied": false,
		"created": false,
		"updated": false,
	}, locale, key, args)
}

func localizedStepWord(locale appi18n.Locale, count int) string {
	if locale != appi18n.Russian {
		if count == 1 {
			return "step"
		}
		return "steps"
	}
	switch appi18n.Plural(locale, count) {
	case appi18n.PluralOne:
		return "шаг"
	case appi18n.PluralFew:
		return "шага"
	default:
		return "шагов"
	}
}

func mapperRecordingReasonKey(reason string) (appi18n.Key, bool) {
	switch reason {
	case "map recording already active":
		return appi18n.MapperRecordingAlreadyActive, true
	case "no active map set":
		return appi18n.MapperRecordingNoActiveMapSet, true
	case "green confirmed anchor required":
		return appi18n.MapperRecordingAnchorRequired, true
	case "complete live room required":
		return appi18n.MapperRecordingLiveRoomRequired, true
	case "reconciliation apply is in progress":
		return appi18n.MapperRecordingReconcileInProgress, true
	case "room autorecord requires an editable map set":
		return appi18n.MapperRecordingMapSetNotEditable, true
	default:
		return "", false
	}
}

func localizedRouteSummary(locale appi18n.Locale, res mapper.PathResult, seams, doors, locked, phrases, deathTraps int) (string, map[string]any) {
	count := len(res.Steps)
	args := map[string]any{
		"steps": count, "seams": seams, "doors": doors, "locked": locked,
		"phrases": phrases, "death_traps": deathTraps,
	}
	return renderMapperWireText(locale, string(appi18n.MapperRouteSummary), args), args
}

// renderMapperWireText is the shared server/client contract for a stable code
// plus typed JSON arguments. Rendering from the wire arguments (including after
// a JSON round trip) guarantees that clients never have to parse human prose to
// choose Russian plurals or optional route-summary clauses.
func renderMapperWireText(locale appi18n.Locale, code string, args map[string]any) string {
	key := appi18n.Key(code)
	if key == appi18n.MapperRouteSummary {
		return renderRouteSummary(locale, args)
	}
	values := make(map[string]string, len(args)+2)
	for name, value := range args {
		values[name] = fmt.Sprint(value)
	}
	if key == appi18n.MapperRecordMovementInProgress {
		count := mapperIntArg(args["count"])
		values["count"] = strconv.Itoa(count)
		values["step_word"] = localizedStepWord(locale, count)
	}
	if key == appi18n.MapperEdgeMovementCommand {
		field := fmt.Sprint(args["field"])
		if locale == appi18n.Russian {
			switch field {
			case "traversal_phrase":
				values["field"] = "фраза перехода"
			case "opening_command":
				values["field"] = "команда открытия"
			}
		} else {
			switch field {
			case "traversal_phrase":
				values["field"] = "traversal phrase"
			case "opening_command":
				values["field"] = "opening command"
			}
		}
		values["value"] = strconv.Quote(fmt.Sprint(args["value"]))
	}
	if key == appi18n.MapperAdminOperationFailed {
		switch fmt.Sprint(args["operation"]) {
		case "check_delete":
			values["operation"] = "check map set deletion"
			if locale == appi18n.Russian {
				values["operation"] = "проверка удаления набора карт"
			}
		case "stage_images":
			values["operation"] = "stage room image cleanup"
			if locale == appi18n.Russian {
				values["operation"] = "подготовка удаления изображений комнат"
			}
		case "restore_images":
			values["operation"] = "restore room image cleanup"
			if locale == appi18n.Russian {
				values["operation"] = "восстановление изображений комнат"
			}
		case "delete":
			values["operation"] = "delete map set"
			if locale == appi18n.Russian {
				values["operation"] = "удаление набора карт"
			}
		}
	}
	if key == appi18n.MapperProfileUnknownRef || key == appi18n.MapperProfileNotCompiled {
		values["ref"] = strconv.Quote(fmt.Sprint(args["ref"]))
	}
	// These codes deliberately keep opaque parser/storage diagnostics as typed
	// data. English stays byte-compatible; Russian exposes authored copy while
	// the caller also returns *_detail for diagnostics.
	if detail, ok := args["detail"].(string); ok && detail != "" {
		switch key {
		case appi18n.MapperImportParseFailed:
			if locale == appi18n.English {
				return detail
			}
		case appi18n.MapperRouteNotFound, appi18n.MapperWalkFailed, appi18n.MapperTopologyWriteFailed:
			if locale == appi18n.English {
				return detail
			}
			if key == appi18n.MapperWalkFailed {
				return "автопереход остановлен"
			}
			if key == appi18n.MapperTopologyWriteFailed {
				return "не удалось изменить топологию"
			}
		case appi18n.MapperInvalidBody:
			if locale == appi18n.English {
				return "invalid body: " + detail
			}
			return "неверное тело запроса"
		case appi18n.MapperZoneMoveFailed:
			if locale == appi18n.English {
				return detail
			}
			return "не удалось перенести зону"
		case appi18n.MapperReconciliationFailed:
			if locale == appi18n.English {
				return detail
			}
			return "не удалось выполнить сверку карты"
		case appi18n.MapperPositionGeneric:
			if locale == appi18n.English {
				return detail
			}
			return "состояние позиции изменилось"
		case appi18n.MapperProfileFallback:
			if locale == appi18n.English {
				return detail
			}
			return "формат комнат не загружен — используется встроенный"
		case appi18n.MapperProfileOperationFailed:
			if locale == appi18n.English {
				return detail
			}
			return "не удалось изменить формат комнат"
		case appi18n.MapperAdminImageCleanupFailed:
			if locale == appi18n.English {
				return detail
			}
			return "набор карт удалён, но изображения комнат удалить не удалось"
		case appi18n.MapperRecordingStopped:
			if locale == appi18n.English {
				return detail
			}
			return "запись карты остановлена"
		}
	}
	return appi18n.Message(locale, key, values)
}

func mapperWalkReason(reason string) (appi18n.Key, map[string]any, string) {
	switch reason {
	case "session not connected — no live tracker":
		return appi18n.MapperSessionNotConnected, nil, ""
	case "session not connected":
		return appi18n.MapperWalkSessionNotConnected, nil, ""
	case "session not connected — no game transport":
		return appi18n.MapperWalkNoGameTransport, nil, ""
	case "no active map set for this session":
		return appi18n.MapperNoActiveMapSet, nil, ""
	case "movement confirmation already pending":
		return appi18n.MapperWalkPendingConfirmation, nil, ""
	case "position lost — use \"I'm here\" to re-anchor":
		return appi18n.MapperPositionLost, nil, ""
	case "already here":
		return appi18n.MapperAlreadyHere, nil, ""
	case "cancelled by user":
		return appi18n.MapperWalkCancelled, nil, ""
	case "no paused route to continue":
		return appi18n.MapperWalkNoPausedRoute, nil, ""
	case "route contains an empty traversal command":
		return appi18n.MapperWalkEmptyTraversal, nil, ""
	case "route crosses a door marked locked; unlock it before auto-walk":
		return appi18n.MapperWalkLockedDoor, nil, ""
	case "door-opening command is a bare movement direction":
		return appi18n.MapperWalkBareOpeningCommand, nil, ""
	case "stopped":
		return appi18n.MapperWalkStopped, nil, ""
	case "completed":
		return appi18n.MapperWalkCompleted, nil, ""
	case "replaced":
		return appi18n.MapperWalkReplaced, nil, ""
	default:
		if reason == "" {
			return "", nil, ""
		}
		return appi18n.MapperWalkFailed, map[string]any{"detail": reason}, reason
	}
}

func localizeMapWalkState(locale appi18n.Locale, state session.MapWalkState) session.MapWalkState {
	key, args, detail := mapperWalkReason(state.Reason)
	if key == "" {
		return state
	}
	state.Reason = renderMapperWireText(locale, string(key), args)
	state.ReasonCode = string(key)
	state.ReasonArgs = args
	if locale != appi18n.English {
		state.ReasonDetail = detail
	}
	return state
}

func localizeMapRecordingState(locale appi18n.Locale, state session.MapRecordingState) session.MapRecordingState {
	if state.Reason != "" {
		key, args, detail := mapperRecordingRuntimeReason(state.Reason)
		state.Reason = renderMapperWireText(locale, string(key), args)
		state.ReasonCode = string(key)
		state.ReasonArgs = args
		if locale != appi18n.English {
			state.ReasonDetail = detail
		}
	}
	if state.Conflict != nil && state.Conflict.Reason != "" {
		conflict := *state.Conflict
		key, args, detail := mapperRecordingRuntimeReason(conflict.Reason)
		conflict.Reason = renderMapperWireText(locale, string(key), args)
		conflict.ReasonCode = string(key)
		conflict.ReasonArgs = args
		if locale != appi18n.English {
			conflict.ReasonDetail = detail
		}
		state.Conflict = &conflict
	}
	return state
}

func mapperRecordingRuntimeReason(reason string) (appi18n.Key, map[string]any, string) {
	switch reason {
	case "stopped by user":
		return appi18n.MapperRecordingStoppedByUser, nil, ""
	case "position lost":
		return appi18n.MapperRecordingPositionLost, nil, ""
	case "previous autorecord step is still pending; wait for the new cell to turn green":
		return appi18n.MapperRecordingPendingLimit, nil, ""
	case "occupied expected room content mismatch":
		return appi18n.MapperRecordingOccupiedMismatch, nil, ""
	case "active map changed":
		return appi18n.MapperRecordingActiveMapChanged, nil, ""
	case "room format changed":
		return appi18n.MapperRecordingRoomFormatChanged, nil, ""
	case "room format detector crashed":
		return appi18n.MapperRecordingDetectorCrashed, nil, ""
	case "manual movement command outcome unknown":
		return appi18n.MapperRecordingManualOutcome, nil, ""
	case "map walk command outcome unknown":
		return appi18n.MapperRecordingWalkOutcome, nil, ""
	case "walk confirmation timeout":
		return appi18n.MapperRecordingWalkTimeout, nil, ""
	case "session disconnected":
		return appi18n.MapperRecordingDisconnected, nil, ""
	case "room autorecord requires an editable map set":
		return appi18n.MapperRecordingMapSetNotEditable, nil, ""
	case "incomplete room event":
		return appi18n.MapperRecordingIncompleteRoom, nil, ""
	case "room event too large":
		return appi18n.MapperRecordingRoomEventTooLarge, nil, ""
	case "map topology changed while autorecord was pending":
		return appi18n.MapperRecordingTopologyChanged, nil, ""
	case "autorecord lifecycle changed":
		return appi18n.MapperRecordingLifecycleChanged, nil, ""
	case "movement confirmation timed out":
		return appi18n.MapperRecordingPendingTimeout, nil, ""
	default:
		return appi18n.MapperRecordingStopped, map[string]any{"detail": reason}, reason
	}
}

func localizeMapperServerMsg(locale appi18n.Locale, msg session.ServerMsg) session.ServerMsg {
	if msg.Type == "map_walk" && msg.WalkReason != "" {
		state := localizeMapWalkState(locale, session.MapWalkState{Reason: msg.WalkReason})
		msg.WalkReason = state.Reason
		msg.WalkReasonCode = state.ReasonCode
		msg.WalkReasonArgs = state.ReasonArgs
		msg.WalkReasonDetail = state.ReasonDetail
	}
	if msg.Type == "map_recording_state" && msg.MapRecording != nil {
		state := localizeMapRecordingState(locale, *msg.MapRecording)
		msg.MapRecording = &state
	}
	if msg.Type == "room_position" && msg.PositionReason != "" {
		key, args, detail := mapperPositionReason(msg.PositionReason)
		msg.PositionReason = renderMapperWireText(locale, string(key), args)
		msg.PositionReasonCode = string(key)
		msg.PositionReasonArgs = args
		if locale != appi18n.English {
			msg.PositionReasonDetail = detail
		}
	}
	if msg.Type == "mapper_profile" && msg.MapperProfileError != "" {
		text, code, args, detail := localizeMapperProfileWarning(locale, msg.MapperProfileError)
		msg.MapperProfileError = text
		msg.MapperProfileErrorCode = code
		msg.MapperProfileErrorArgs = args
		msg.MapperProfileErrorDetail = detail
	}
	return msg
}

func mapperPositionReason(reason string) (appi18n.Key, map[string]any, string) {
	switch reason {
	case "no active map set":
		return appi18n.MapperPositionNoActiveMapSet, nil, ""
	case "no position yet":
		return appi18n.MapperPositionNoPositionYet, nil, ""
	case "index rebuilt — position reset":
		return appi18n.MapperPositionIndexRebuilt, nil, ""
	case "anchored to unmapped cell":
		return appi18n.MapperPositionAnchoredUnmapped, nil, ""
	default:
		return appi18n.MapperPositionGeneric, map[string]any{"detail": reason}, reason
	}
}

func localizeMapperProfileWarning(locale appi18n.Locale, warning string) (string, string, map[string]any, string) {
	if warning == "" {
		return "", "", nil, ""
	}
	args := map[string]any{"detail": warning}
	detail := ""
	if locale != appi18n.English {
		detail = warning
	}
	return renderMapperWireText(locale, string(appi18n.MapperProfileFallback), args), string(appi18n.MapperProfileFallback), args, detail
}

func addMapperProfileWarning(out map[string]any, locale appi18n.Locale, warning string) {
	text, code, args, detail := localizeMapperProfileWarning(locale, warning)
	out["error"] = text
	if code == "" {
		return
	}
	out["error_code"] = code
	out["error_args"] = args
	if detail != "" {
		out["error_detail"] = detail
	}
}

func mapperIntArg(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := strconv.Atoi(string(value))
		return parsed
	case string:
		parsed, _ := strconv.Atoi(value)
		return parsed
	default:
		return 0
	}
}

func renderRouteSummary(locale appi18n.Locale, args map[string]any) string {
	count := mapperIntArg(args["steps"])
	parts := []string{appi18n.Message(locale, appi18n.MapperRouteSummary, map[string]string{
		"count": strconv.Itoa(count), "step_word": localizedStepWord(locale, count),
	})}
	clauses := []struct {
		arg string
		key appi18n.Key
	}{
		{"seams", appi18n.MapperRouteSummarySeams},
		{"doors", appi18n.MapperRouteSummaryDoors},
		{"locked", appi18n.MapperRouteSummaryLocked},
		{"phrases", appi18n.MapperRouteSummaryPhrases},
		{"death_traps", appi18n.MapperRouteSummaryDeathTraps},
	}
	for _, clause := range clauses {
		count := mapperIntArg(args[clause.arg])
		if count > 0 {
			parts = append(parts, appi18n.Message(locale, clause.key, map[string]string{"count": strconv.Itoa(count)}))
		}
	}
	return strings.Join(parts, ", ")
}
