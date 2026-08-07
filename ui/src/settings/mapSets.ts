export function mapArchiveFileError(file: File | null | undefined): string {
  if (!file) return 'Choose a ZIP archive.';
  if (!file.name.toLowerCase().endsWith('.zip')) return 'Only .zip map archives are supported.';
  return '';
}

export function importFailureMessage(payload: unknown, fallback: string): string {
  if (payload && typeof payload === 'object' && 'error' in payload && typeof payload.error === 'string' && payload.error) {
    return payload.error;
  }
  return fallback;
}

export function formatMapImportConflict(conflict: {
  kind: string;
  zone?: string;
  x: number;
  y: number;
  l: number;
  source?: string;
  other_source?: string;
}): string {
  const parts = [conflict.kind];
  if (conflict.zone) parts.push(conflict.zone);
  // Coordinates identify a room collision. A duplicate zone has no meaningful
  // room coordinate, and showing its zero-value tuple is misleading.
  if (conflict.kind === 'duplicate_room') parts.push(`(${conflict.x}, ${conflict.y}, ${conflict.l})`);
  if (conflict.source) parts.push(`first: ${conflict.source}`);
  if (conflict.other_source) parts.push(`again: ${conflict.other_source}`);
  return parts.join(' - ');
}

export class MapSetListRevision {
  private revision = 0;

  begin(): number {
    this.revision += 1;
    return this.revision;
  }

  isCurrent(request: number): boolean {
    return request === this.revision;
  }
}
