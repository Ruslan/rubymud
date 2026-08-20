package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"rubymud/go/internal/mapimport"
)

const (
	autorecordMaxBatchID = 128
	autorecordMaxOrigin  = 256
	autorecordMaxCommand = 512
	autorecordMaxHint    = 512
	autorecordMaxDesc    = 4096
	autorecordMaxExits   = 1024
	// A concurrent WAL reader can make a deferred read-then-write transaction
	// return SQLITE_BUSY_SNAPSHOT (517) when it upgrades its snapshot. Retry the
	// whole bounded transaction in the worker; never make the parser wait on it.
	autorecordBusyRetries = 5
)

// autorecordRoomWithSnapshot is retained for callers that need the arm by
// itself. The normal ApplyTopologyOp path calls the transaction-aware helper
// below so the topology revision read, room/edge mutation, and revision bump
// are retried as one outer transaction.
func (s *Store) autorecordRoomWithSnapshot(mapSetID int64, in AutorecordInput) (before TopologyBefore, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		before, found, err = s.autorecordRoomWithSnapshotTx(tx, mapSetID, in)
		return err
	})
	return before, found, err
}

// autorecordRoomWithSnapshotTx is the storage arm for the first continuous
// autorecord slice. It deliberately does not open a nested transaction: the
// caller owns the outer transaction which also reads and advances the topology
// revision. This is important for SQLITE_BUSY_SNAPSHOT retry correctness.
func (s *Store) autorecordRoomWithSnapshotTx(tx *gorm.DB, mapSetID int64, in AutorecordInput) (before TopologyBefore, found bool, err error) {
	in.Source.Zone, err = NormalizeZoneName(in.Source.Zone)
	if err != nil {
		return before, false, err
	}
	in.Target.Zone, err = NormalizeZoneName(in.Target.Zone)
	if err != nil {
		return before, false, err
	}
	in.Dir = strings.ToUpper(strings.TrimSpace(in.Dir))
	if _, ok := dirDelta[in.Dir]; !ok {
		return before, false, fmt.Errorf("invalid autorecord direction")
	}
	if in.Source == in.Target {
		return before, false, fmt.Errorf("autorecord source and target must differ")
	}
	if strings.TrimSpace(in.Command) == "" || len([]rune(in.Command)) > autorecordMaxCommand {
		return before, false, fmt.Errorf("autorecord command is required")
	}
	if in.Fields.Hint == nil || strings.TrimSpace(*in.Fields.Hint) == "" || len([]rune(*in.Fields.Hint)) > autorecordMaxHint {
		return before, false, fmt.Errorf("autorecord room hint is required")
	}
	if in.Fields.Desc == nil || in.Fields.Exits == nil {
		return before, false, fmt.Errorf("autorecord room block is incomplete")
	}
	if len([]rune(*in.Fields.Desc)) > autorecordMaxDesc || len([]rune(*in.Fields.Exits)) > autorecordMaxExits {
		return before, false, fmt.Errorf("autorecord room block is too large")
	}
	if len([]rune(in.RecordBatchID)) == 0 || len([]rune(in.RecordBatchID)) > autorecordMaxBatchID {
		return before, false, fmt.Errorf("autorecord batch id is invalid")
	}
	if len([]rune(in.RecordOrigin)) > autorecordMaxOrigin {
		return before, false, fmt.Errorf("autorecord origin is too long")
	}
	if in.VerificationState == "" {
		in.VerificationState = "provisional"
	}
	if in.RecordSource == "" {
		in.RecordSource = EdgeSourceAutorecord
	}
	if in.VerificationState != "provisional" || in.RecordSource != EdgeSourceAutorecord {
		return before, false, fmt.Errorf("invalid autorecord provenance")
	}

	run := func() error {
		before = TopologyBefore{}
		found = false
		sourceSnap, e := snapshotRoom(tx, mapSetID, in.Source.Zone, in.Source.X, in.Source.Y, in.Source.L)
		if e != nil {
			return e
		}
		if !sourceSnap.Existed {
			return fmt.Errorf("autorecord source room not found")
		}
		targetSnap, e := snapshotRoom(tx, mapSetID, in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L)
		if e != nil {
			return e
		}
		before.Rooms = []RoomSnapshot{sourceSnap, targetSnap}
		before.Manual, e = captureManualEdgesTx(tx, mapSetID, []EdgeCell{in.Source, in.Target})
		if e != nil {
			return e
		}

		// Capture both directional faces even when absent. The reverse face may be
		// materialized from the newly observed room's exit text and is then removed
		// by the write as a non-evidence tombstone; undo must remove that tombstone.
		for _, face := range []struct {
			cell EdgeCell
			dir  string
		}{
			{in.Source, in.Dir},
			{in.Target, oppositeDir[in.Dir]},
		} {
			var row MapEdge
			fe := tx.Where("map_set_id=? AND kind IN ? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?",
				mapSetID, []string{EdgeKindGrid, EdgeKindCorridor}, face.cell.Zone, face.cell.X, face.cell.Y, face.cell.L, face.dir).First(&row).Error
			if fe != nil && fe != gorm.ErrRecordNotFound {
				return fe
			}
			before.Faces = append(before.Faces, DirectionalFaceSnapshot{Cell: face.cell, Dir: face.dir, Existed: fe == nil, Edge: row})
		}
		if targetSnap.Existed {
			// A replay of the exact committed batch is a storage-level no-op. A
			// different occupant is a refusal: never overwrite an existing room
			// merely because its text happens to look similar.
			fingerprint := mapRoomFieldsFingerprint(in.Fields)
			var edge MapEdge
			ee := tx.Where("map_set_id=? AND kind=? AND source=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=? AND to_zone=? AND to_x=? AND to_y=? AND to_l=?",
				mapSetID, EdgeKindCorridor, EdgeSourceAutorecord,
				in.Source.Zone, in.Source.X, in.Source.Y, in.Source.L, in.Dir,
				in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L).First(&edge).Error
			if ee == nil && targetSnap.Room.RecordSource == EdgeSourceAutorecord && targetSnap.Room.RecordBatchID == in.RecordBatchID && targetSnap.Room.Fingerprint == fingerprint {
				found = true
				return nil
			}
			return fmt.Errorf("autorecord target room is occupied")
		}
		// Autorecord may replace a derived grid face with the exact corridor it
		// just proved, but it must never erase a hand-drawn/reconciled assertion
		// (or an older exact corridor) while doing so. Refuse before the room
		// upsert, so the operation remains fully atomic and the caller can enter
		// the existing conflict/reconciliation path instead. Exact replay was
		// handled above, so its own corridor/tombstone is intentionally allowed
		// to remain a storage-level no-op.
		for _, face := range before.Faces {
			if face.Existed && !autorecordFaceReplaceable(face.Edge) {
				return fmt.Errorf("autorecord would overwrite an authoritative %s %s face", face.Edge.Source, face.Dir)
			}
		}

		// Reuse the canonical room-field normalizer. It creates only the target
		// room in this same outer transaction; provenance is applied immediately
		// below before the exact edge is materialized.
		inner := &Store{db: tx}
		if _, _, e = inner.upsertRoomWithSnapshot(mapSetID, in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L, in.Fields); e != nil {
			return e
		}
		recordedAt := nowSQLiteTime()
		if e = tx.Model(&Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L).Updates(map[string]any{
			"verification_state": in.VerificationState,
			"record_source":      in.RecordSource,
			"record_batch_id":    in.RecordBatchID,
			"recorded_at":        recordedAt,
			"record_origin":      in.RecordOrigin,
		}).Error; e != nil {
			return e
		}

		// Replace any derived source face with the exact recorded corridor. The
		// observed target's reverse exit is evidence about text, not evidence that
		// the player has already walked back, so own that reverse face with a
		// reconciled blocked tombstone when derivation produced one.
		if e = tx.Where("map_set_id=? AND kind IN ? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?",
			mapSetID, []string{EdgeKindGrid, EdgeKindCorridor}, in.Source.Zone, in.Source.X, in.Source.Y, in.Source.L, in.Dir).Delete(&MapEdge{}).Error; e != nil {
			return e
		}
		reverseDir := oppositeDir[in.Dir]
		var reverse MapEdge
		re := tx.Where("map_set_id=? AND kind=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?",
			mapSetID, EdgeKindGrid, in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L, reverseDir).First(&reverse).Error
		if re != nil && re != gorm.ErrRecordNotFound {
			return re
		}
		if re == nil && reverse.Passable {
			if e = tx.Delete(&MapEdge{}, reverse.ID).Error; e != nil {
				return e
			}
			blocked := reverse
			blocked.ID = 0
			blocked.Source = EdgeSourceReconciled
			blocked.Passable = false
			blocked.Command = ""
			blocked.EdgeAttrs = EdgeAttrs{Weight: 1}
			if e = tx.Create(&blocked).Error; e != nil {
				return e
			}
		}

		z, x, y, l := in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L
		row := MapEdge{
			MapSetID: mapSetID, Kind: EdgeKindCorridor, Source: EdgeSourceAutorecord,
			Passable: true, FromZone: in.Source.Zone, FromX: in.Source.X, FromY: in.Source.Y, FromL: in.Source.L,
			Dir: in.Dir, ToZone: &z, ToX: &x, ToY: &y, ToL: &l, Command: in.Command,
			EdgeAttrs: EdgeAttrs{Weight: 1},
		}
		if e = tx.Create(&row).Error; e != nil {
			return e
		}
		found = true
		return nil
	}
	err = run()
	if err != nil {
		return before, false, err
	}
	return before, found, nil
}

func autorecordFaceReplaceable(edge MapEdge) bool {
	return edge.Kind == EdgeKindGrid && (edge.Source == EdgeSourceParsed || edge.Source == EdgeSourceGrid)
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func mapRoomFieldsFingerprint(fields RoomFields) string {
	var hint, desc, exits string
	if fields.Hint != nil {
		hint = *fields.Hint
	}
	if fields.Desc != nil {
		desc = *fields.Desc
	}
	if fields.Exits != nil {
		exits = *fields.Exits
	}
	return mapimport.Fingerprint(hint, desc, exits)
}
