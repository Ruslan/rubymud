package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// stagedMapSetImageDir holds a renamed image directory while the DB delete is
// in progress. Renaming within one filesystem is atomic, so a failed DB
// transaction can put the directory back before the request returns.
type stagedMapSetImageDir struct {
	original  string
	tombstone string
}

func (s *Server) stageMapSetImageDir(mapSetID int64) (stagedMapSetImageDir, error) {
	if mapSetID <= 0 || s.roomImagesRoot == "" {
		return stagedMapSetImageDir{}, nil
	}
	rootInfo, err := os.Lstat(filepath.Clean(s.roomImagesRoot))
	if errors.Is(err, os.ErrNotExist) {
		return stagedMapSetImageDir{}, nil
	}
	if err != nil {
		return stagedMapSetImageDir{}, fmt.Errorf("inspect room images root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return stagedMapSetImageDir{}, errors.New("room images root must be a real directory")
	}
	name := strconv.FormatInt(mapSetID, 10)
	original, err := mapSetImagePath(s.roomImagesRoot, name)
	if err != nil {
		return stagedMapSetImageDir{}, err
	}
	if _, err := os.Lstat(original); err != nil {
		if os.IsNotExist(err) {
			return stagedMapSetImageDir{}, nil
		}
		return stagedMapSetImageDir{}, fmt.Errorf("inspect room images: %w", err)
	}

	placeholder, err := os.CreateTemp(s.roomImagesRoot, ".deleting-"+name+"-")
	if err != nil {
		return stagedMapSetImageDir{}, fmt.Errorf("prepare room image cleanup: %w", err)
	}
	tombstone := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		_ = os.Remove(tombstone)
		return stagedMapSetImageDir{}, fmt.Errorf("prepare room image cleanup: %w", err)
	}
	if err := os.Remove(tombstone); err != nil {
		return stagedMapSetImageDir{}, fmt.Errorf("prepare room image cleanup: %w", err)
	}
	if err := os.Rename(original, tombstone); err != nil {
		return stagedMapSetImageDir{}, fmt.Errorf("stage room image cleanup: %w", err)
	}
	return stagedMapSetImageDir{original: original, tombstone: tombstone}, nil
}

func mapSetImagePath(root, name string) (string, error) {
	root = filepath.Clean(root)
	if name == "" || filepath.Base(name) != name {
		return "", errors.New("invalid room image path")
	}
	path := filepath.Join(root, name)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid room image path")
	}
	return path, nil
}

// recoverMapSetImageTombstones completes a deletion interrupted between the
// pre-transaction rename and final RemoveAll. A surviving map-set row means the
// transaction did not commit, so its directory is restored; a missing row means
// the committed deletion wins and the tombstone is removed.
func (s *Server) recoverMapSetImageTombstones() error {
	if s.roomImagesRoot == "" {
		return nil
	}
	root := filepath.Clean(s.roomImagesRoot)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect room images root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("room images root must be a real directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read room images root: %w", err)
	}
	for _, entry := range entries {
		id, ok := tombstoneMapSetID(entry.Name())
		if !ok {
			continue
		}
		tombstone, err := mapSetImagePath(root, entry.Name())
		if err != nil {
			return err
		}
		if _, err := s.store.GetMapSet(id); err == nil {
			original, err := mapSetImagePath(root, strconv.FormatInt(id, 10))
			if err != nil {
				return err
			}
			if _, err := os.Lstat(original); err == nil {
				return fmt.Errorf("cannot restore room image tombstone for map set %d: destination exists", id)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect room image restore destination: %w", err)
			}
			if err := os.Rename(tombstone, original); err != nil {
				return fmt.Errorf("restore room image tombstone: %w", err)
			}
			continue
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check map set for room image tombstone: %w", err)
		}
		if err := os.RemoveAll(tombstone); err != nil {
			return fmt.Errorf("remove room image tombstone: %w", err)
		}
	}
	return nil
}

func tombstoneMapSetID(name string) (int64, bool) {
	const prefix = ".deleting-"
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(name, prefix)
	idText, suffix, ok := strings.Cut(rest, "-")
	if !ok || idText == "" || suffix == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || idText != strconv.FormatInt(id, 10) {
		return 0, false
	}
	return id, true
}

func (staged stagedMapSetImageDir) rollback() error {
	if staged.tombstone == "" {
		return nil
	}
	if err := os.Rename(staged.tombstone, staged.original); err != nil {
		return fmt.Errorf("restore room images after database failure: %w", err)
	}
	return nil
}

// finalize runs only after the database transaction has committed. An error
// leaves a hidden tombstone for manual cleanup; the logical map-set deletion is
// already complete and must not be reported as a retryable failed delete.
func (staged stagedMapSetImageDir) finalize() error {
	if staged.tombstone == "" {
		return nil
	}
	if err := os.RemoveAll(staged.tombstone); err != nil {
		return fmt.Errorf("remove staged room images: %w", err)
	}
	return nil
}
