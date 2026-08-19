import { settingsMapperI18n, type SettingsMapperI18n } from './mapperI18n';

export function mapArchiveFileError(file: File | null | undefined, i18n: SettingsMapperI18n = settingsMapperI18n): string {
  if (!file) return i18n.t('chooseZip');
  if (!file.name.toLowerCase().endsWith('.zip')) return i18n.t('onlyZip');
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
}, i18n: SettingsMapperI18n = settingsMapperI18n): string {
  const kind = conflict.kind === 'duplicate_zone'
    ? i18n.t('duplicateZone')
    : conflict.kind === 'duplicate_room' ? i18n.t('duplicateRoom') : conflict.kind;
  const parts = [kind];
  if (conflict.zone) parts.push(conflict.zone);
  // Coordinates identify a room collision. A duplicate zone has no meaningful
  // room coordinate, and showing its zero-value tuple is misleading.
  if (conflict.kind === 'duplicate_room') parts.push(`(${conflict.x}, ${conflict.y}, ${conflict.l})`);
  if (conflict.source) parts.push(i18n.t('conflictFirst', { source: conflict.source }));
  if (conflict.other_source) parts.push(i18n.t('conflictAgain', { source: conflict.other_source }));
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
