import { describe, expect, it } from 'vitest';

import {
  createMapperI18n,
  mapperCatalogKeys,
  mapperPlaceholders,
  mapperPluralKeys,
  mapperPluralPlaceholders,
} from './i18n';
import { formatExitDiff, recordAction, recordButtonState, statusHeaderText } from './cellMenu';
import { attrsSummary, dirName, emptyEdgeAttrs, provenanceLabel } from './edgeEditor';
import { previewSummary } from './zoneCrud';
import type { LiveRoom, PlayerPosition } from './types';

describe('mapper i18n catalog', () => {
  it('keeps the en/ru keys, placeholders and plural forms complete', () => {
    expect(mapperCatalogKeys('ru')).toEqual(mapperCatalogKeys('en'));
    for (const key of mapperCatalogKeys('en')) {
      expect(mapperPlaceholders('ru', key)).toEqual(mapperPlaceholders('en', key));
    }
    expect(mapperPluralKeys('ru')).toEqual(mapperPluralKeys('en'));
    for (const key of mapperPluralKeys('en')) {
      expect(mapperPluralPlaceholders('ru', key)).toEqual(mapperPluralPlaceholders('en', key));
    }
  });

  it('uses Russian plural rules rather than an English singular suffix', () => {
    const ru = createMapperI18n('ru');
    expect([1, 2, 5, 11, 21, 24].map((count) => ru.plural('step', count)))
      .toEqual(['шаг', 'шага', 'шагов', 'шагов', 'шаг', 'шага']);
    expect(ru.plural('room', 22)).toBe('22 комнаты');
    expect(createMapperI18n('en').plural('room', 2)).toBe('2 rooms');
    expect([1, 2, 5].map((count) => ru.plural('lockedWarning', count))).toEqual([
      '⚠ 1 дверь помечена запертой — нужен ключ',
      '⚠ 2 двери помечены запертыми — нужен ключ',
      '⚠ 5 дверей помечены запертыми — нужен ключ',
    ]);
  });

  it('keeps exactly one coordinate-brace pair in record tooltips', () => {
    const pos: PlayerPosition = {
      valid: true, zone: 'Z', x: 4, y: 7, l: 2, confidence: 'green',
      pendingMoves: 0,
    };
    const live: LiveRoom = { hint: 'Atrium', desc: '', exits: 'N' };
    expect(recordButtonState(pos, live, false, createMapperI18n('en')).title)
      .toBe("create cell {Z 4,7,2} from live room 'Atrium'");
    expect(recordButtonState(pos, live, false, createMapperI18n('ru')).title)
      .toBe('создать клетку {Z 4,7,2} из живой комнаты «Atrium»');
  });

  it('keeps unresolved seams distinct from blocked or forbidden targets', () => {
    expect(createMapperI18n('en').t('targetUnresolved')).toBe('Target room is unresolved');
    expect(createMapperI18n('ru').t('targetUnresolved')).toBe('Целевая комната не определена');
    expect(createMapperI18n('en').t('seamUnresolved')).toBe('Seam target is unresolved');
    expect(createMapperI18n('ru').t('seamUnresolved')).toBe('Цель перехода не определена');
  });

  it('translates authored copy but preserves room names, commands and directions as values', () => {
    const ru = createMapperI18n('ru');
    expect(ru.t('transitionAction', { command: 'open north', zone: 'Северный край' }))
      .toBe('Переход: open north → Северный край');
    expect(ru.t('recordFailed', { reason: 'MUD says: запад' }))
      .toBe('Не удалось записать комнату: MUD says: запад');
  });

  it('localizes the pure mapper decision helpers without changing their gates', () => {
    const ru = createMapperI18n('ru');
    const pos: PlayerPosition = {
      valid: true, zone: 'Зона', x: 1, y: 2, l: 0, confidence: 'yellow',
      reason: 'raw tracker detail', exitsAddedLive: ['U'], exitsRemovedMap: ['N'],
      pendingMoves: 2,
    };
    const live: LiveRoom = { hint: 'Комната', desc: '', exits: 'U' };
    expect(statusHeaderText(null, ru)).toBe('⚪ нет позиции');
    expect(formatExitDiff(pos, ru)).toBe('выходы: +U в игре / −N на карте');
    expect(recordAction(pos, live, { z: 'Зона', x: 1, y: 2, l: 0 }, false, ru).reason)
      .toContain('2 шага');
    expect(provenanceLabel('derived', ru)).toBe('проход разобран из текста комнаты');
    expect(dirName('W', ru)).toBe('запад');
    expect(attrsSummary({ ...emptyEdgeAttrs, locked: true }, 'N', '', ru)).toContain('нужен ключ');
  });

  it('renders zone summaries through the plural API in both locales', () => {
    const preview = {
      revision: 1, rooms: 22, internal_edges: 5, boundary_grid_edges: 1,
      portals: 2, incoming: 1, outgoing: 3, cross_zone_contacts: 4,
      components: 2, disconnected: true, annotations_moved: 0,
      collision_count: 5, source_empties: false, target_exists: false,
    };
    expect(previewSummary(preview, createMapperI18n('ru'))).toContain('22 комнаты');
    expect(previewSummary(preview, createMapperI18n('ru'))).toContain('5 коллизий');
    expect(previewSummary(preview, createMapperI18n('en'))).toContain('22 rooms');
  });
});
