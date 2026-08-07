import { describe, expect, it } from 'vitest';
import { formatMapImportConflict, importFailureMessage, mapArchiveFileError, MapSetListRevision } from './mapSets';

describe('map-set upload helpers', () => {
  it('accepts ZIP filenames case-insensitively and rejects every other extension', () => {
    expect(mapArchiveFileError(new File(['zip'], 'world.ZIP'))).toBe('');
    expect(mapArchiveFileError(new File(['rar'], 'world.rar'))).toBe('Only .zip map archives are supported.');
    expect(mapArchiveFileError(null)).toBe('Choose a ZIP archive.');
  });

  it('shows the structured import error text when the server supplied one', () => {
    expect(importFailureMessage({ error: 'duplicate rooms', conflicts: [] }, 'upload failed')).toBe('duplicate rooms');
    expect(importFailureMessage({}, 'upload failed')).toBe('upload failed');
  });

  it('formats only room conflicts with coordinates and retains both source labels', () => {
    expect(formatMapImportConflict({ kind: 'duplicate_zone', zone: 'A', x: 0, y: 0, l: 0, source: 'one.mm2', other_source: 'two.mm2' }))
      .toBe('duplicate_zone - A - first: one.mm2 - again: two.mm2');
    expect(formatMapImportConflict({ kind: 'duplicate_room', zone: 'A', x: 2, y: -1, l: 4, source: 'one.mm2', other_source: 'two.mm2' }))
      .toBe('duplicate_room - A - (2, -1, 4) - first: one.mm2 - again: two.mm2');
  });

  it('ignores a stale list response after a newer refresh starts', () => {
    const revision = new MapSetListRevision();
    const first = revision.begin();
    const second = revision.begin();
    expect(revision.isCurrent(first)).toBe(false);
    expect(revision.isCurrent(second)).toBe(true);
  });
});
