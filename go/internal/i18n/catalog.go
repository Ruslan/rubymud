// Package i18n provides the small, dependency-free catalog used by authored
// RubyMUD messages. Game output, map data and commands must never pass through
// this package.
package i18n

import (
	"regexp"
	"strings"
)

type Locale string

const (
	English Locale = "en"
	Russian Locale = "ru"
)

type Key string

const (
	MapperNoPosition                   Key = "mapper.no_position"
	MapperRecordFailed                 Key = "mapper.record_failed"
	MapperSessionNotConnected          Key = "mapper.session_not_connected"
	MapperNoActiveMapSet               Key = "mapper.no_active_map_set"
	MapperPositionLost                 Key = "mapper.position_lost"
	MapperAlreadyHere                  Key = "mapper.already_here"
	MapperTargetDeathTrap              Key = "mapper.target_death_trap"
	MapperRouteNotFound                Key = "mapper.route_not_found"
	MapperRouteVetoedOnly              Key = "mapper.route_vetoed_only"
	MapperRouteSummary                 Key = "mapper.route_summary"
	MapperRouteSummarySeams            Key = "mapper.route_summary.seams"
	MapperRouteSummaryDoors            Key = "mapper.route_summary.doors"
	MapperRouteSummaryLocked           Key = "mapper.route_summary.locked"
	MapperRouteSummaryPhrases          Key = "mapper.route_summary.phrases"
	MapperRouteSummaryDeathTraps       Key = "mapper.route_summary.death_traps"
	MapperRecordNoLiveRoom             Key = "mapper.record.no_live_room"
	MapperRecordPositionUnknown        Key = "mapper.record.position_unknown"
	MapperRecordPositionLost           Key = "mapper.record.position_lost"
	MapperRecordCellMismatch           Key = "mapper.record.cell_mismatch"
	MapperRecordNamelessRoom           Key = "mapper.record.nameless_room"
	MapperRecordMovementInProgress     Key = "mapper.record.movement_in_progress"
	MapperRecordWrongCell              Key = "mapper.record.wrong_cell"
	MapperRecordWriteNotApplied        Key = "mapper.record.write_not_applied"
	MapperRecordingAlreadyActive       Key = "mapper.recording.already_active"
	MapperRecordingAnchorRequired      Key = "mapper.recording.anchor_required"
	MapperRecordingLiveRoomRequired    Key = "mapper.recording.live_room_required"
	MapperRecordingReconcileInProgress Key = "mapper.recording.reconcile_in_progress"
	MapperRecordingSessionNotConnected Key = "mapper.recording.session_not_connected"
	MapperRecordingNoActiveMapSet      Key = "mapper.recording.no_active_map_set"
	MapperRecordingStoppedByUser       Key = "mapper.recording.stopped_by_user"
	MapperRecordingPositionLost        Key = "mapper.recording.position_lost"
	MapperRecordingPendingLimit        Key = "mapper.recording.pending_limit"
	MapperRecordingOccupiedMismatch    Key = "mapper.recording.occupied_mismatch"
	MapperRecordingActiveMapChanged    Key = "mapper.recording.active_map_changed"
	MapperRecordingRoomFormatChanged   Key = "mapper.recording.room_format_changed"
	MapperRecordingDetectorCrashed     Key = "mapper.recording.detector_crashed"
	MapperRecordingManualOutcome       Key = "mapper.recording.manual_outcome_unknown"
	MapperRecordingWalkOutcome         Key = "mapper.recording.walk_outcome_unknown"
	MapperRecordingWalkTimeout         Key = "mapper.recording.walk_timeout"
	MapperRecordingDisconnected        Key = "mapper.recording.disconnected"
	MapperRecordingMapSetNotEditable   Key = "mapper.recording.map_set_not_editable"
	MapperRecordingIncompleteRoom      Key = "mapper.recording.incomplete_room"
	MapperRecordingRoomEventTooLarge   Key = "mapper.recording.room_event_too_large"
	MapperRecordingTopologyChanged     Key = "mapper.recording.topology_changed"
	MapperRecordingLifecycleChanged    Key = "mapper.recording.lifecycle_changed"
	MapperRecordingPendingTimeout      Key = "mapper.recording.pending_timeout"
	MapperRecordingObscured            Key = "mapper.recording.room_obscured"
	MapperRecordingStopped             Key = "mapper.recording.stopped"
	MapperRoomNotFound                 Key = "mapper.room_not_found"
	MapperDeleteCurrentRoom            Key = "mapper.delete.current_room"
	MapperDeleteRoomNotLeaf            Key = "mapper.delete.not_leaf"
	MapperInvalidBody                  Key = "mapper.invalid_body"
	MapperTopologyWriteFailed          Key = "mapper.topology_write_failed"
	MapperEdgeWeightPositive           Key = "mapper.edge.weight_positive"
	MapperEdgeMovementCommand          Key = "mapper.edge.movement_command"
	MapperWalkSessionNotConnected      Key = "mapper.walk.session_not_connected"
	MapperWalkNoGameTransport          Key = "mapper.walk.no_game_transport"
	MapperWalkPendingConfirmation      Key = "mapper.walk.pending_confirmation"
	MapperWalkCancelled                Key = "mapper.walk.cancelled"
	MapperWalkNoPausedRoute            Key = "mapper.walk.no_paused_route"
	MapperWalkEmptyTraversal           Key = "mapper.walk.empty_traversal"
	MapperWalkLockedDoor               Key = "mapper.walk.locked_door"
	MapperWalkBareOpeningCommand       Key = "mapper.walk.bare_opening_command"
	MapperWalkStopped                  Key = "mapper.walk.stopped"
	MapperWalkCompleted                Key = "mapper.walk.completed"
	MapperWalkReplaced                 Key = "mapper.walk.replaced"
	MapperWalkFailed                   Key = "mapper.walk.failed"
	MapperBadDirection                 Key = "mapper.edge.bad_direction"
	MapperBadMark                      Key = "mapper.edge.bad_mark"
	MapperEdgeNeedsRooms               Key = "mapper.edge.needs_rooms"
	MapperEdgeSelectorRequired         Key = "mapper.edge.selector_required"
	MapperEdgeNothingToSet             Key = "mapper.edge.nothing_to_set"
	MapperEdgePassageMissing           Key = "mapper.edge.passage_missing"
	MapperPortalSourceRequired         Key = "mapper.portal.source_required"
	MapperPortalOrderCreateForbidden   Key = "mapper.portal.order_create_forbidden"
	MapperPortalTargetCommandRequired  Key = "mapper.portal.target_command_required"
	MapperPortalOrderRequired          Key = "mapper.portal.order_required"
	MapperPortalTargetRequired         Key = "mapper.portal.target_required"
	MapperPortalDeleteFieldsForbidden  Key = "mapper.portal.delete_fields_forbidden"
	MapperPortalNotFound               Key = "mapper.portal.not_found"
	MapperNothingToUndo                Key = "mapper.undo.nothing"
	MapperUndoTargetMissing            Key = "mapper.undo.target_missing"
	MapperUndoRestoreFailed            Key = "mapper.undo.restore_failed"
	MapperImportRequestTooLarge        Key = "mapper.import.request_too_large"
	MapperImportInvalidMultipart       Key = "mapper.import.invalid_multipart"
	MapperImportMissingArchive         Key = "mapper.import.missing_archive"
	MapperImportZipRequired            Key = "mapper.import.zip_required"
	MapperImportArchiveTooLarge        Key = "mapper.import.archive_too_large"
	MapperImportReadFailed             Key = "mapper.import.read_failed"
	MapperImportParseFailed            Key = "mapper.import.parse_failed"
	MapperImportStoreFailed            Key = "mapper.import.store_failed"
	MapperProfileMissingSpec           Key = "mapper.profile.missing_spec"
	MapperProfileNotFound              Key = "mapper.profile.not_found"
	MapperProfileBuiltinReadOnly       Key = "mapper.profile.builtin_read_only"
	MapperProfileValidationFailed      Key = "mapper.profile.validation_failed"
	MapperProfileSessionNotFound       Key = "mapper.profile.session_not_found"
	MapperProfileReplayLimit           Key = "mapper.profile.replay_limit"
	MapperProfileFallback              Key = "mapper.profile.fallback"
	MapperProfileInvalidID             Key = "mapper.profile.invalid_id"
	MapperProfileUnknownRef            Key = "mapper.profile.unknown_ref"
	MapperProfileNotCompiled           Key = "mapper.profile.not_compiled"
	MapperProfileNameRequired          Key = "mapper.profile.name_required"
	MapperProfileNameTaken             Key = "mapper.profile.name_taken"
	MapperProfileOperationFailed       Key = "mapper.profile.operation_failed"
	MapperZoneSourceTargetRequired     Key = "mapper.zone.source_target_required"
	MapperZoneRenameAllRooms           Key = "mapper.zone.rename_all_rooms"
	MapperZoneRoomsRequired            Key = "mapper.zone.rooms_required"
	MapperZoneExpectedRevision         Key = "mapper.zone.expected_revision"
	MapperZoneMoveNotApplied           Key = "mapper.zone.not_applied"
	MapperZoneMoveFailed               Key = "mapper.zone.failed"
	MapperReconciliationConflictToken  Key = "mapper.reconciliation.conflict_token_required"
	MapperReconciliationFailed         Key = "mapper.reconciliation.failed"
	MapperAdminInvalidMapSetID         Key = "mapper.admin.invalid_map_set_id"
	MapperAdminMapSetNotFound          Key = "mapper.admin.map_set_not_found"
	MapperAdminMapSetIDNotFound        Key = "mapper.admin.map_set_id_not_found"
	MapperAdminSessionNotFound         Key = "mapper.admin.session_not_found"
	MapperAdminDeleteForksFirst        Key = "mapper.admin.delete_forks_first"
	MapperAdminOperationFailed         Key = "mapper.admin.operation_failed"
	MapperAdminImageCleanupFailed      Key = "mapper.admin.image_cleanup_failed"
	MapperPositionNoActiveMapSet       Key = "mapper.position.no_active_map_set"
	MapperPositionNoPositionYet        Key = "mapper.position.no_position_yet"
	MapperPositionIndexRebuilt         Key = "mapper.position.index_rebuilt"
	MapperPositionAnchoredUnmapped     Key = "mapper.position.anchored_unmapped"
	MapperPositionObscured             Key = "mapper.position.room_obscured"
	MapperPositionObscuredUnknown      Key = "mapper.position.room_obscured_unknown"
	MapperPositionGeneric              Key = "mapper.position.status"
	MapperInvalidSessionID             Key = "mapper.invalid_session_id"
	MapperTrailingData                 Key = "mapper.trailing_data"
	MapperReconciliationDisconnected   Key = "mapper.reconciliation.session_not_connected"
)

var catalogs = map[Locale]map[Key]string{
	English: {
		MapperNoPosition:                   "no position",
		MapperRecordFailed:                 "Failed to record room: {reason}",
		MapperSessionNotConnected:          "session not connected — no live tracker",
		MapperNoActiveMapSet:               "no active map set for this session",
		MapperPositionLost:                 "position lost — use \"I'm here\" to re-anchor",
		MapperAlreadyHere:                  "already here",
		MapperTargetDeathTrap:              "target is a death trap",
		MapperRouteNotFound:                "no route to target in the active set",
		MapperRouteVetoedOnly:              "a route exists only through edges marked \"avoid in routes\"",
		MapperRouteSummary:                 "{count} {step_word}",
		MapperRouteSummarySeams:            "seams: {count}",
		MapperRouteSummaryDoors:            "doors: {count} (opening included in route)",
		MapperRouteSummaryLocked:           "⚠ locked by default: {count}",
		MapperRouteSummaryPhrases:          "phrase passages: {count}",
		MapperRouteSummaryDeathTraps:       "⚠ death traps on route: {count}",
		MapperRecordNoLiveRoom:             "no live room seen yet — walk into the room (or look) first",
		MapperRecordPositionUnknown:        "position unknown — use \"I'm here\" to re-anchor first",
		MapperRecordPositionLost:           "position lost (🔴) — the live room describes another cell; use \"I'm here\" first",
		MapperRecordCellMismatch:           "the live room does not match this cell (position is held by dead reckoning) — use \"I'm here\" first",
		MapperRecordNamelessRoom:           "the latest room block has no name (corridor or exits only) — recording would erase the name and description",
		MapperRecordMovementInProgress:     "movement in progress ({count} queued {step_word}) — recording would clear the queue; wait until it finishes",
		MapperRecordWrongCell:              "can only record the cell you are standing in (you are at {coord})",
		MapperRecordWriteNotApplied:        "the write did not apply to the active map set",
		MapperRecordingAlreadyActive:       "map recording already active",
		MapperRecordingAnchorRequired:      "green confirmed anchor required",
		MapperRecordingLiveRoomRequired:    "complete live room required",
		MapperRecordingReconcileInProgress: "reconciliation apply is in progress",
		MapperRecordingSessionNotConnected: "session not connected",
		MapperRecordingNoActiveMapSet:      "no active map set",
		MapperRecordingStoppedByUser:       "stopped by user",
		MapperRecordingPositionLost:        "position lost",
		MapperRecordingPendingLimit:        "previous autorecord step is still pending; wait for the new cell to turn green",
		MapperRecordingOccupiedMismatch:    "occupied expected room content mismatch",
		MapperRecordingActiveMapChanged:    "active map changed",
		MapperRecordingRoomFormatChanged:   "room format changed",
		MapperRecordingDetectorCrashed:     "room format detector crashed",
		MapperRecordingManualOutcome:       "manual movement command outcome unknown",
		MapperRecordingWalkOutcome:         "map walk command outcome unknown",
		MapperRecordingWalkTimeout:         "walk confirmation timeout",
		MapperRecordingDisconnected:        "session disconnected",
		MapperRecordingMapSetNotEditable:   "room autorecord requires an editable map set",
		MapperRecordingIncompleteRoom:      "incomplete room event",
		MapperRecordingRoomEventTooLarge:   "room event too large",
		MapperRecordingTopologyChanged:     "map topology changed while autorecord was pending",
		MapperRecordingLifecycleChanged:    "autorecord lifecycle changed",
		MapperRecordingPendingTimeout:      "movement confirmation timed out",
		MapperRecordingObscured:            "room obscured; restore visibility and restart OBS",
		MapperRecordingStopped:             "{detail}",
		MapperRoomNotFound:                 "room not found in the active map set",
		MapperDeleteCurrentRoom:            "the current room cannot be deleted — move to its neighbour first",
		MapperDeleteRoomNotLeaf:            "room is not a leaf — it has more than one mapped neighbour",
		MapperInvalidBody:                  "invalid body: {detail}",
		MapperTopologyWriteFailed:          "{detail}",
		MapperEdgeWeightPositive:           "edge weight must be finite and greater than zero",
		MapperEdgeMovementCommand:          "{field} {value} is a movement command (direction {direction}): the route would send it, the tracker would count it as a step, and your position would drift. Use a command that is not a bare direction.",
		MapperWalkSessionNotConnected:      "session not connected",
		MapperWalkNoGameTransport:          "session not connected — no game transport",
		MapperWalkPendingConfirmation:      "movement confirmation already pending",
		MapperWalkCancelled:                "cancelled by user",
		MapperWalkNoPausedRoute:            "no paused route to continue",
		MapperWalkEmptyTraversal:           "route contains an empty traversal command",
		MapperWalkLockedDoor:               "route crosses a door marked locked; unlock it before auto-walk",
		MapperWalkBareOpeningCommand:       "door-opening command is a bare movement direction",
		MapperWalkStopped:                  "stopped",
		MapperWalkCompleted:                "completed",
		MapperWalkReplaced:                 "replaced",
		MapperWalkFailed:                   "{detail}",
		MapperBadDirection:                 "bad direction",
		MapperBadMark:                      "bad mark",
		MapperEdgeNeedsRooms:               "a hand-drawn edge needs a room on BOTH sides of the boundary",
		MapperEdgeSelectorRequired:         "provide exactly one valid direction or seam selector",
		MapperEdgeNothingToSet:             "nothing to set",
		MapperEdgePassageMissing:           "the directed passage is missing or the selected seam is ambiguous",
		MapperPortalSourceRequired:         "zone,x,y,l are required",
		MapperPortalOrderCreateForbidden:   "order is not allowed when creating a portal",
		MapperPortalTargetCommandRequired:  "target zone,x,y,l and command are required",
		MapperPortalOrderRequired:          "non-negative portal order is required",
		MapperPortalTargetRequired:         "target requires zone,x,y,l",
		MapperPortalDeleteFieldsForbidden:  "delete accepts only source coordinates and order",
		MapperPortalNotFound:               "source, target, or selected portal occurrence was not found",
		MapperNothingToUndo:                "nothing to undo on this map set",
		MapperUndoTargetMissing:            "target cell {coord} no longer exists",
		MapperUndoRestoreFailed:            "the recorded before-state could not be restored",
		MapperImportRequestTooLarge:        "archive request too large",
		MapperImportInvalidMultipart:       "invalid multipart form",
		MapperImportMissingArchive:         "missing archive file",
		MapperImportZipRequired:            "map archive must be a .zip file",
		MapperImportArchiveTooLarge:        "archive too large",
		MapperImportReadFailed:             "read archive failed",
		MapperImportParseFailed:            "failed to parse map archive",
		MapperImportStoreFailed:            "failed to store map set",
		MapperProfileMissingSpec:           "missing spec",
		MapperProfileNotFound:              "mapper profile not found",
		MapperProfileBuiltinReadOnly:       "the builtin room format is read-only — make a copy of it instead",
		MapperProfileValidationFailed:      "{detail}",
		MapperProfileSessionNotFound:       "session not found",
		MapperProfileReplayLimit:           "limit must be between 1 and {max}",
		MapperProfileFallback:              "{detail}",
		MapperProfileInvalidID:             "invalid mapper profile id",
		MapperProfileUnknownRef:            "unknown room format reference {ref}",
		MapperProfileNotCompiled:           "room format {ref} is not compiled into this build",
		MapperProfileNameRequired:          "mapper profile: name is required",
		MapperProfileNameTaken:             "mapper profile: a profile with this name already exists",
		MapperProfileOperationFailed:       "{detail}",
		MapperZoneSourceTargetRequired:     "source_zone and target_zone are required",
		MapperZoneRenameAllRooms:           "rename selects the complete source zone server-side",
		MapperZoneRoomsRequired:            "rooms is required for a partial move",
		MapperZoneExpectedRevision:         "expected_revision is required for apply",
		MapperZoneMoveNotApplied:           "zone move did not apply",
		MapperZoneMoveFailed:               "{detail}",
		MapperReconciliationConflictToken:  "conflict_id and token are required",
		MapperReconciliationFailed:         "{detail}",
		MapperAdminInvalidMapSetID:         "invalid map set id",
		MapperAdminMapSetNotFound:          "map set not found",
		MapperAdminMapSetIDNotFound:        "map set {id} not found",
		MapperAdminSessionNotFound:         "session {id} not found",
		MapperAdminDeleteForksFirst:        "delete editable forks first",
		MapperAdminOperationFailed:         "{operation} failed",
		MapperAdminImageCleanupFailed:      "{detail}",
		MapperPositionNoActiveMapSet:       "no active map set",
		MapperPositionNoPositionYet:        "no position yet",
		MapperPositionIndexRebuilt:         "index rebuilt — position reset",
		MapperPositionAnchoredUnmapped:     "anchored to unmapped cell",
		MapperPositionObscured:             "room obscured; restore visibility",
		MapperPositionObscuredUnknown:      "room obscured; position unknown until visibility is restored",
		MapperPositionGeneric:              "{detail}",
		MapperInvalidSessionID:             "invalid session id",
		MapperTrailingData:                 "trailing data",
		MapperReconciliationDisconnected:   "session not connected",
	},
	Russian: {
		MapperNoPosition:                   "нет позиции",
		MapperRecordFailed:                 "Не удалось записать комнату: {reason}",
		MapperSessionNotConnected:          "сессия не подключена — нет живого трекера",
		MapperNoActiveMapSet:               "для этой сессии не выбран набор карт",
		MapperPositionLost:                 "позиция потеряна — сначала «Я здесь»",
		MapperAlreadyHere:                  "вы уже здесь",
		MapperTargetDeathTrap:              "целевая комната помечена как death trap",
		MapperRouteNotFound:                "нет маршрута к цели в активном наборе карт",
		MapperRouteVetoedOnly:              "маршрут есть, но только через грани с пометкой «не водить сюда» — снимите пометку в редакторе ребра, если туда всё-таки можно",
		MapperRouteSummary:                 "{count} {step_word}",
		MapperRouteSummarySeams:            "швов: {count}",
		MapperRouteSummaryDoors:            "дверей: {count} (открытие включено в путь)",
		MapperRouteSummaryLocked:           "⚠ заперто по умолчанию: {count}",
		MapperRouteSummaryPhrases:          "проходов фразой: {count}",
		MapperRouteSummaryDeathTraps:       "⚠ death trap на пути: {count}",
		MapperRecordNoLiveRoom:             "живая комната ещё не получена — войдите в комнату или осмотритесь",
		MapperRecordPositionUnknown:        "позиция неизвестна — сначала «Я здесь»",
		MapperRecordPositionLost:           "позиция потеряна (🔴) — живая комната описывает не эту клетку; сначала «Я здесь»",
		MapperRecordCellMismatch:           "живая комната не соответствует этой клетке (позиция удерживается по счислению) — сначала «Я здесь»",
		MapperRecordNamelessRoom:           "в последнем блоке комнаты нет названия (коридор или только выходы) — запись стёрла бы название и описание",
		MapperRecordMovementInProgress:     "идёт перемещение ({count} {step_word} в пути) — запись сбросила бы очередь; дождитесь конца",
		MapperRecordWrongCell:              "можно записать только клетку, в которой вы стоите (вы в {coord})",
		MapperRecordWriteNotApplied:        "изменение не применилось к активному набору карт",
		MapperRecordingAlreadyActive:       "запись карты уже включена",
		MapperRecordingAnchorRequired:      "нужна подтверждённая зелёная позиция — сначала «Я здесь»",
		MapperRecordingLiveRoomRequired:    "нужен полный живой блок комнаты",
		MapperRecordingReconcileInProgress: "применяется разрешение конфликта карты",
		MapperRecordingSessionNotConnected: "сессия не подключена",
		MapperRecordingNoActiveMapSet:      "нет активного набора карт",
		MapperRecordingStoppedByUser:       "остановлено пользователем",
		MapperRecordingPositionLost:        "запись карты остановлена: позиция потеряна",
		MapperRecordingPendingLimit:        "предыдущий шаг автозаписи ещё не завершён; дождитесь, пока новая клетка станет зелёной",
		MapperRecordingOccupiedMismatch:    "содержимое занятой целевой комнаты не совпало — нужна сверка карты",
		MapperRecordingActiveMapChanged:    "запись карты остановлена: активный набор карт изменён",
		MapperRecordingRoomFormatChanged:   "запись карты остановлена: формат комнат изменён",
		MapperRecordingDetectorCrashed:     "запись карты остановлена: обработчик формата комнат аварийно завершился",
		MapperRecordingManualOutcome:       "запись карты остановлена: результат ручного перемещения неизвестен",
		MapperRecordingWalkOutcome:         "запись карты остановлена: результат команды автоперехода неизвестен",
		MapperRecordingWalkTimeout:         "запись карты остановлена: подтверждение автоперехода не получено вовремя",
		MapperRecordingDisconnected:        "запись карты остановлена: сессия отключена",
		MapperRecordingMapSetNotEditable:   "пошаговая запись комнат требует редактируемого набора карт",
		MapperRecordingIncompleteRoom:      "запись карты остановлена: блок комнаты неполный",
		MapperRecordingRoomEventTooLarge:   "запись карты остановлена: блок комнаты слишком велик",
		MapperRecordingTopologyChanged:     "запись карты остановлена: топология набора карт изменилась",
		MapperRecordingLifecycleChanged:    "запись карты остановлена: состояние сессии изменилось",
		MapperRecordingPendingTimeout:      "запись карты остановлена: подтверждение перемещения не получено вовремя",
		MapperRecordingObscured:            "запись карты остановлена: комната не видна — восстановите освещение и снова включите НАБЛ",
		MapperRecordingStopped:             "запись карты остановлена: {detail}",
		MapperRoomNotFound:                 "комната не найдена в активном наборе карт",
		MapperDeleteCurrentRoom:            "нельзя удалить текущую комнату — сначала перейдите в соседнюю",
		MapperDeleteRoomNotLeaf:            "комната не листовая — у неё больше одного разведанного соседа",
		MapperInvalidBody:                  "неверное тело запроса: {detail}",
		MapperTopologyWriteFailed:          "не удалось изменить топологию: {detail}",
		MapperEdgeWeightPositive:           "вес перехода должен быть конечным положительным числом",
		MapperEdgeMovementCommand:          "{field} {value} является командой движения (направление {direction}): маршрут отправил бы её, трекер посчитал бы её шагом, и позиция сместилась бы. Укажите команду, которая не является отдельным направлением.",
		MapperWalkSessionNotConnected:      "сессия не подключена",
		MapperWalkNoGameTransport:          "сессия не подключена — нет игрового транспорта",
		MapperWalkPendingConfirmation:      "ожидается подтверждение предыдущего перемещения",
		MapperWalkCancelled:                "остановлено пользователем",
		MapperWalkNoPausedRoute:            "нет приостановленного маршрута для продолжения",
		MapperWalkEmptyTraversal:           "в маршруте есть пустая команда перехода",
		MapperWalkLockedDoor:               "маршрут проходит через запертую дверь — откройте её перед автопереходом",
		MapperWalkBareOpeningCommand:       "команда открытия двери является отдельным направлением движения",
		MapperWalkStopped:                  "остановлено",
		MapperWalkCompleted:                "завершено",
		MapperWalkReplaced:                 "заменено новым маршрутом",
		MapperWalkFailed:                   "автопереход остановлен: {detail}",
		MapperBadDirection:                 "неверное направление",
		MapperBadMark:                      "неверная ручная пометка",
		MapperEdgeNeedsRooms:               "для ручного ребра нужны комнаты по обе стороны границы",
		MapperEdgeSelectorRequired:         "укажите ровно одно допустимое направление или селектор перехода",
		MapperEdgeNothingToSet:             "не указано ни одного изменения",
		MapperEdgePassageMissing:           "направленный проход отсутствует или выбранный шов неоднозначен",
		MapperPortalSourceRequired:         "нужны zone,x,y,l исходной клетки",
		MapperPortalOrderCreateForbidden:   "при создании портала нельзя указывать order",
		MapperPortalTargetCommandRequired:  "нужны zone,x,y,l цели и команда перехода",
		MapperPortalOrderRequired:          "нужен неотрицательный order портала",
		MapperPortalTargetRequired:         "для цели нужны zone,x,y,l",
		MapperPortalDeleteFieldsForbidden:  "при удалении допустимы только координаты исходной клетки и order",
		MapperPortalNotFound:               "исходная клетка, цель или выбранный портал не найдены",
		MapperNothingToUndo:                "Нечего отменять на этом наборе карт",
		MapperUndoTargetMissing:            "целевая клетка {coord} больше не существует",
		MapperUndoRestoreFailed:            "не удалось восстановить записанное предыдущее состояние",
		MapperImportRequestTooLarge:        "запрос с архивом слишком большой",
		MapperImportInvalidMultipart:       "неверный multipart-запрос",
		MapperImportMissingArchive:         "файл архива не передан",
		MapperImportZipRequired:            "архив карты должен быть файлом .zip",
		MapperImportArchiveTooLarge:        "архив слишком большой",
		MapperImportReadFailed:             "не удалось прочитать архив",
		MapperImportParseFailed:            "Не удалось разобрать архив карты",
		MapperImportStoreFailed:            "не удалось сохранить набор карт",
		MapperProfileMissingSpec:           "не задано описание формата комнат",
		MapperProfileNotFound:              "формат комнат не найден",
		MapperProfileBuiltinReadOnly:       "встроенный формат комнат доступен только для чтения — сначала создайте копию",
		MapperProfileValidationFailed:      "Описание формата некорректно: {detail}",
		MapperProfileSessionNotFound:       "сессия не найдена",
		MapperProfileReplayLimit:           "limit должен быть от 1 до {max}",
		MapperProfileFallback:              "формат комнат не загружен — используется встроенный: {detail}",
		MapperProfileInvalidID:             "неверный идентификатор формата комнат",
		MapperProfileUnknownRef:            "неизвестная ссылка на формат комнат {ref}",
		MapperProfileNotCompiled:           "формат комнат {ref} не включён в эту сборку",
		MapperProfileNameRequired:          "нужно название формата комнат",
		MapperProfileNameTaken:             "формат комнат с таким названием уже существует",
		MapperProfileOperationFailed:       "не удалось изменить формат комнат: {detail}",
		MapperZoneSourceTargetRequired:     "нужны исходная и целевая зоны",
		MapperZoneRenameAllRooms:           "переименование выбирает всю исходную зону на сервере",
		MapperZoneRoomsRequired:            "для частичного переноса нужен список комнат",
		MapperZoneExpectedRevision:         "для применения нужна expected_revision",
		MapperZoneMoveNotApplied:           "перенос зоны не применён",
		MapperZoneMoveFailed:               "не удалось перенести зону: {detail}",
		MapperReconciliationConflictToken:  "нужны conflict_id и token",
		MapperReconciliationFailed:         "не удалось выполнить сверку карты: {detail}",
		MapperAdminInvalidMapSetID:         "неверный идентификатор набора карт",
		MapperAdminMapSetNotFound:          "набор карт не найден",
		MapperAdminMapSetIDNotFound:        "набор карт {id} не найден",
		MapperAdminSessionNotFound:         "сессия {id} не найдена",
		MapperAdminDeleteForksFirst:        "сначала удалите редактируемые копии",
		MapperAdminOperationFailed:         "не удалось выполнить операцию «{operation}»",
		MapperAdminImageCleanupFailed:      "набор карт удалён, но изображения комнат удалить не удалось: {detail}",
		MapperPositionNoActiveMapSet:       "нет активного набора карт",
		MapperPositionNoPositionYet:        "позиция ещё не определена",
		MapperPositionIndexRebuilt:         "индекс карты перестроен — позиция сброшена",
		MapperPositionAnchoredUnmapped:     "позиция привязана к клетке вне карты",
		MapperPositionObscured:             "комната не видна — восстановите освещение",
		MapperPositionObscuredUnknown:      "комната не видна — позиция неизвестна до восстановления освещения",
		MapperPositionGeneric:              "состояние позиции изменилось: {detail}",
		MapperInvalidSessionID:             "неверный идентификатор сессии",
		MapperTrailingData:                 "лишние данные после JSON",
		MapperReconciliationDisconnected:   "сессия не подключена",
	},
}

func SupportedLocales() []Locale {
	return []Locale{English, Russian}
}

// ParseLocale validates the exact values accepted from persistent settings and
// the settings API. Browser language tags should use NormalizeLocale instead.
func ParseLocale(value string) (Locale, bool) {
	switch value {
	case string(English):
		return English, true
	case string(Russian):
		return Russian, true
	default:
		return English, false
	}
}

// NormalizeLocale accepts common browser language tags and safely falls back
// to English for absent, corrupt or unsupported values.
func NormalizeLocale(value string) Locale {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	if base, _, ok := strings.Cut(normalized, "-"); ok {
		normalized = base
	}
	if locale, ok := ParseLocale(normalized); ok {
		return locale
	}
	return English
}

var interpolationPattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

func Message(locale Locale, key Key, values map[string]string) string {
	catalog, ok := catalogs[locale]
	if !ok {
		catalog = catalogs[English]
	}
	message, ok := catalog[key]
	if !ok {
		message, ok = catalogs[English][key]
	}
	if !ok {
		return string(key)
	}
	return interpolationPattern.ReplaceAllStringFunc(message, func(placeholder string) string {
		name := placeholder[1 : len(placeholder)-1]
		if value, found := values[name]; found {
			return value
		}
		return placeholder
	})
}

type PluralForm uint8

const (
	PluralOne PluralForm = iota
	PluralFew
	PluralMany
)

func Plural(locale Locale, count int) PluralForm {
	if locale != Russian {
		if count == 1 {
			return PluralOne
		}
		return PluralMany
	}
	mod10, mod100 := count%10, count%100
	if mod10 == 1 && mod100 != 11 {
		return PluralOne
	}
	if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
		return PluralFew
	}
	return PluralMany
}
