package storage

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// LayoutMutation is an explicit sparse presentation mutation. Nil Layout removes
// an override; a non-nil value replaces it literally after its logical key has
// been canonicalized from Cell.
type LayoutMutation struct {
	Cell   EdgeCell
	Layout *RoomLayout
}

// ReconciliationInput is the revision-fenced storage half of conflict resolve.
// It deliberately contains no session token: session ownership belongs to C2.
type ReconciliationInput struct {
	Source           EdgeCell
	Dir              string
	Target           EdgeCell
	Command          string
	Phrase           string
	SourceProvenance string
	Attrs            EdgeAttrs
	Layouts          []LayoutMutation
	ExpectedRevision int64
}

// ApplyReconciliation atomically replaces one directional face with one exact,
// directed corridor and applies the caller's explicit sparse layout preview.
func (s *Store) ApplyReconciliation(mapSetID int64, in ReconciliationInput) (before TopologyBefore, err error) {
	in.Source.Zone, err = NormalizeZoneName(in.Source.Zone)
	if err != nil {
		return before, err
	}
	in.Target.Zone, err = NormalizeZoneName(in.Target.Zone)
	if err != nil {
		return before, err
	}
	in.Dir = strings.ToUpper(strings.TrimSpace(in.Dir))
	if _, ok := dirDelta[in.Dir]; !ok {
		return before, fmt.Errorf("invalid corridor direction")
	}
	if in.SourceProvenance == "" {
		in.SourceProvenance = EdgeSourceReconciled
	}
	if in.SourceProvenance != EdgeSourceReconciled && in.SourceProvenance != EdgeSourceManual {
		return before, fmt.Errorf("invalid corridor source")
	}
	if strings.TrimSpace(in.Command) == "" {
		return before, fmt.Errorf("corridor command is required")
	}
	if in.Source == in.Target {
		return before, fmt.Errorf("corridor source and target must differ")
	}
	if in.Attrs.Weight != 0 && !ValidExplicitWeight(in.Attrs.Weight) {
		return before, fmt.Errorf("invalid corridor weight")
	}
	if len(in.Layouts) > 8000 {
		return before, fmt.Errorf("too many layout changes")
	}

	apply := func() error {
		before = TopologyBefore{}
		return s.db.Transaction(func(tx *gorm.DB) error {
			rev, e := topologyRevisionTx(tx, mapSetID)
			if e != nil {
				return e
			}
			if s.topologyApplyAttemptHook != nil {
				if hookErr := s.topologyApplyAttemptHook(); hookErr != nil {
					return hookErr
				}
			}
			if rev != in.ExpectedRevision {
				return fmt.Errorf("stale topology revision: expected %d, current %d", in.ExpectedRevision, rev)
			}
			for _, c := range []EdgeCell{in.Source, in.Target} {
				var n int64
				if e := tx.Model(&Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, c.Zone, c.X, c.Y, c.L).Count(&n).Error; e != nil {
					return e
				}
				if n != 1 {
					return fmt.Errorf("corridor endpoint room not found")
				}
			}
			before.Revision = rev
			var old MapEdge
			e = tx.Where("map_set_id=? AND kind IN ? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", mapSetID, []string{EdgeKindGrid, EdgeKindCorridor}, in.Source.Zone, in.Source.X, in.Source.Y, in.Source.L, in.Dir).First(&old).Error
			if e != nil && e != gorm.ErrRecordNotFound {
				return e
			}
			before.Faces = []DirectionalFaceSnapshot{{Cell: in.Source, Dir: in.Dir, Existed: e == nil, Edge: old}}
			// Reconciliation replaces evidence for one formerly assumed physical pair.
			// Remove the old reverse half only when it was derived solely from the other
			// room's side: a permissive/non-advertising source has not independently
			// confirmed that directed traversal. Manual/corridor rows and a reverse room
			// that explicitly advertises its own direction remain authoritative.
			var staleReverse *MapEdge
			oldDerived := e == nil && old.Kind == EdgeKindGrid && old.Passable &&
				(old.Source == EdgeSourceParsed || old.Source == EdgeSourceGrid)
			if oldDerived {
				if oldTarget, ok := old.To(); ok && oldTarget == in.Source.step(in.Dir) {
					// A derived grid face must point at its literal adjacent cell. Do not
					// infer a pair from a mismatched/imported exact target.
					reverseDir := oppositeDir[in.Dir]
					var reverse MapEdge
					rerr := tx.Where("map_set_id=? AND kind=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", mapSetID, EdgeKindGrid, oldTarget.Zone, oldTarget.X, oldTarget.Y, oldTarget.L, reverseDir).First(&reverse).Error
					if rerr != nil && rerr != gorm.ErrRecordNotFound {
						return rerr
					}
					reverseTarget, reverseHasTarget := reverse.To()
					reverseDerivedPair := rerr == nil && reverse.Passable &&
						(reverse.Source == EdgeSourceParsed || reverse.Source == EdgeSourceGrid) &&
						reverseHasTarget && reverseTarget == in.Source
					if reverseDerivedPair {
						var reverseRoom Room
						if rerr = tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, oldTarget.Zone, oldTarget.X, oldTarget.Y, oldTarget.L).First(&reverseRoom).Error; rerr != nil {
							return rerr
						}
						if !RoomHasExplicitExits(reverseRoom.Ch, decodeStrArray(reverseRoom.EDirs)) || !RoomExitsInDir(reverseRoom.Ch, decodeStrArray(reverseRoom.EDirs), reverseDir) {
							copy := reverse
							staleReverse = &copy
							before.Faces = append(before.Faces, DirectionalFaceSnapshot{Cell: oldTarget, Dir: reverseDir, Existed: true, Edge: reverse})
						}
					}
				}
			}

			seen := map[EdgeCell]bool{}
			for i := range in.Layouts {
				m := &in.Layouts[i]
				m.Cell.Zone, e = NormalizeZoneName(m.Cell.Zone)
				if e != nil {
					return e
				}
				if seen[m.Cell] {
					return fmt.Errorf("duplicate layout mutation")
				}
				seen[m.Cell] = true
				var room Room
				if e = tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, m.Cell.Zone, m.Cell.X, m.Cell.Y, m.Cell.L).First(&room).Error; e != nil {
					return fmt.Errorf("layout logical room not found: %w", e)
				}
				var prior RoomLayout
				e = tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, m.Cell.Zone, m.Cell.X, m.Cell.Y, m.Cell.L).First(&prior).Error
				if e != nil && e != gorm.ErrRecordNotFound {
					return e
				}
				before.Layouts = append(before.Layouts, RoomLayoutSnapshot{Cell: m.Cell, Existed: e == nil, Layout: prior})
				if e == nil && prior.Pinned {
					moves := m.Layout == nil && (prior.DisplayX != m.Cell.X || prior.DisplayY != m.Cell.Y || prior.DisplayL != m.Cell.L)
					moves = moves || (m.Layout != nil && (prior.DisplayX != m.Layout.DisplayX || prior.DisplayY != m.Layout.DisplayY || prior.DisplayL != m.Layout.DisplayL))
					if moves {
						return fmt.Errorf("pinned room cannot be moved")
					}
				}
			}
			if e = tx.Where("map_set_id=? AND kind IN ? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", mapSetID, []string{EdgeKindGrid, EdgeKindCorridor}, in.Source.Zone, in.Source.X, in.Source.Y, in.Source.L, in.Dir).Delete(&MapEdge{}).Error; e != nil {
				return e
			}
			if staleReverse != nil {
				if e = tx.Delete(&MapEdge{}, staleReverse.ID).Error; e != nil {
					return e
				}
				// Keep an authoritative, non-rendered/non-routable face tombstone. A
				// plain delete is insufficient: room derivation is symmetric and would
				// recreate this disproven reverse on the next upsert/rematerialization.
				// Reconciled distinguishes it from a user-authored red/manual wall.
				blocked := *staleReverse
				blocked.ID = 0
				blocked.Source = EdgeSourceReconciled
				blocked.Passable = false
				blocked.Command = ""
				blocked.EdgeAttrs = EdgeAttrs{Weight: 1}
				if e = tx.Create(&blocked).Error; e != nil {
					return e
				}
			}
			attrs := in.Attrs
			if attrs.Weight == 0 {
				attrs.Weight = 1
			}
			attrs.Phrase = in.Phrase
			z, x, y, l := in.Target.Zone, in.Target.X, in.Target.Y, in.Target.L
			edge := MapEdge{MapSetID: mapSetID, Kind: EdgeKindCorridor, Source: in.SourceProvenance, Passable: true, FromZone: in.Source.Zone, FromX: in.Source.X, FromY: in.Source.Y, FromL: in.Source.L, Dir: in.Dir, ToZone: &z, ToX: &x, ToY: &y, ToL: &l, Command: in.Command, EdgeAttrs: attrs}
			if e = tx.Create(&edge).Error; e != nil {
				return e
			}
			for _, m := range in.Layouts {
				if e = tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, m.Cell.Zone, m.Cell.X, m.Cell.Y, m.Cell.L).Delete(&RoomLayout{}).Error; e != nil {
					return e
				}
				if m.Layout != nil {
					v := *m.Layout
					v.MapSetID = mapSetID
					v.Zone = m.Cell.Zone
					v.X = m.Cell.X
					v.Y = m.Cell.Y
					v.L = m.Cell.L
					v.UpdatedAt = *nowSQLiteTimePtr()
					if e = tx.Create(&v).Error; e != nil {
						return e
					}
				}
			}
			if e = validateEffectiveLayoutTx(tx, mapSetID); e != nil {
				return e
			}
			return advanceTopologyRevisionTx(tx, mapSetID, rev)
		})
	}
	for attempt := 0; ; attempt++ {
		err = apply()
		if err == nil || !isSQLiteBusy(err) || attempt >= autorecordBusyRetries-1 {
			break
		}
		time.Sleep(time.Duration(5*(1<<attempt)) * time.Millisecond)
	}
	if err != nil {
		return TopologyBefore{}, err
	}
	return before, nil
}

func validateEffectiveLayoutTx(tx *gorm.DB, mapSetID int64) error {
	var rooms []Room
	if err := tx.Where("map_set_id=?", mapSetID).Find(&rooms).Error; err != nil {
		return err
	}
	var layouts []RoomLayout
	if err := tx.Where("map_set_id=?", mapSetID).Find(&layouts).Error; err != nil {
		return err
	}
	by := map[EdgeCell]RoomLayout{}
	for _, l := range layouts {
		by[EdgeCell{l.Zone, l.X, l.Y, l.L}] = l
	}
	used := map[EdgeCell]EdgeCell{}
	for _, r := range rooms {
		logical := EdgeCell{r.Zone, r.X, r.Y, r.L}
		display := logical
		if l, ok := by[logical]; ok {
			display = EdgeCell{r.Zone, l.DisplayX, l.DisplayY, l.DisplayL}
		}
		if p, ok := used[display]; ok && p != logical {
			return fmt.Errorf("display collision")
		}
		used[display] = logical
	}
	return nil
}

func (s *Store) ValidateReconciliationPreview(mapSetID int64, target EdgeCell, layouts []LayoutMutation) error {
	var err error
	if target.Zone, err = NormalizeZoneName(target.Zone); err != nil {
		return err
	}
	if len(layouts) > 0 {
		layouts = append([]LayoutMutation(nil), layouts...)
	}
	for i := range layouts {
		if layouts[i].Cell.Zone, err = NormalizeZoneName(layouts[i].Cell.Zone); err != nil {
			return err
		}
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, target.Zone, target.X, target.Y, target.L).Count(&n).Error; err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("target room not found")
		}
		for _, m := range layouts {
			var rn int64
			if err := tx.Model(&Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, m.Cell.Zone, m.Cell.X, m.Cell.Y, m.Cell.L).Count(&rn).Error; err != nil {
				return err
			}
			if rn != 1 {
				return fmt.Errorf("layout logical room not found")
			}
			if m.Layout == nil {
				return fmt.Errorf("layout value is required")
			}
		}
		// Apply validation against an in-memory effective view without writes.
		var rooms []Room
		if err := tx.Where("map_set_id=?", mapSetID).Find(&rooms).Error; err != nil {
			return err
		}
		var existing []RoomLayout
		if err := tx.Where("map_set_id=?", mapSetID).Find(&existing).Error; err != nil {
			return err
		}
		by := map[EdgeCell]RoomLayout{}
		for _, l := range existing {
			by[EdgeCell{l.Zone, l.X, l.Y, l.L}] = l
		}
		seen := map[EdgeCell]bool{}
		for _, m := range layouts {
			if seen[m.Cell] {
				return fmt.Errorf("duplicate layout mutation")
			}
			seen[m.Cell] = true
			if prior, ok := by[m.Cell]; ok && prior.Pinned && (prior.DisplayX != m.Layout.DisplayX || prior.DisplayY != m.Layout.DisplayY || prior.DisplayL != m.Layout.DisplayL) {
				return fmt.Errorf("pinned room cannot be moved")
			}
			v := *m.Layout
			v.Zone = m.Cell.Zone
			v.X = m.Cell.X
			v.Y = m.Cell.Y
			v.L = m.Cell.L
			by[m.Cell] = v
		}
		used := map[EdgeCell]bool{}
		for _, r := range rooms {
			c := EdgeCell{r.Zone, r.X, r.Y, r.L}
			d := c
			if l, ok := by[c]; ok {
				d = EdgeCell{r.Zone, l.DisplayX, l.DisplayY, l.DisplayL}
			}
			if used[d] {
				return fmt.Errorf("display collision")
			}
			used[d] = true
		}
		return nil
	})
}
