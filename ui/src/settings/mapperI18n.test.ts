import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import ts from 'typescript';
import { parse } from 'svelte/compiler';
import { describe, expect, it } from 'vitest';

import {
  createSettingsMapperI18n,
  settingsMapperCatalogKeys,
  settingsMapperPlaceholders,
  settingsMapperPluralKeys,
  settingsMapperPluralPlaceholders,
} from './mapperI18n';
import { formatMapImportConflict, mapArchiveFileError } from './mapSets';
import { mapperProfileLabel, mapperProvenanceLabel, mapperProvenanceNote } from './mapperProfileForm';

describe('settings mapper i18n catalog', () => {
  it('keeps en/ru keys, placeholders, and plural forms complete', () => {
    expect(settingsMapperCatalogKeys('ru')).toEqual(settingsMapperCatalogKeys('en'));
    for (const key of settingsMapperCatalogKeys('en')) {
      expect(settingsMapperPlaceholders('ru', key)).toEqual(settingsMapperPlaceholders('en', key));
    }
    expect(settingsMapperPluralKeys('ru')).toEqual(settingsMapperPluralKeys('en'));
    for (const key of settingsMapperPluralKeys('en')) {
      expect(settingsMapperPluralPlaceholders('ru', key)).toEqual(settingsMapperPluralPlaceholders('en', key));
    }
  });

  it('localizes helper copy while preserving archive sources and persisted provenance identities', () => {
    const ru = createSettingsMapperI18n('ru');
    expect(mapArchiveFileError(null, ru)).toBe('Выберите ZIP-архив.');
    expect(formatMapImportConflict({
      kind: 'duplicate_zone', zone: 'Север', x: 0, y: 0, l: 0,
      source: 'сырой.mm2', other_source: 'again.mm2',
    }, ru)).toBe('дубликат зоны - Север - первый источник: сырой.mm2 - повторно: again.mm2');
    const profile = { name: 'diku-en', title: 'Diku English', builtin: true, provenance: 'Verified' } as never;
    expect(mapperProfileLabel(profile, ru)).toBe('Diku English (встроенный)');
    expect(mapperProvenanceNote(profile, ru)).toBe('Проверен — испытан на реальном сервере.');
    expect(mapperProvenanceLabel(profile, ru)).toBe('Проверен');
    expect(profile.provenance).toBe('Verified');
    expect(mapperProvenanceNote({ ...profile, provenance: 'Starter' } as never, ru))
      .toBe('Заготовка — правдоподобный формат, который никто не проверял на реальном сервере. Скопируйте и настройте его.');
    expect(mapperProvenanceNote({ ...profile, provenance: 'Contributed' } as never, ru))
      .toBe('Внесённый Go-детектор — корректность подтверждается только тестовыми примерами.');
  });

  it('uses Russian count forms without modifying names or references', () => {
    const ru = createSettingsMapperI18n('ru');
    expect([1, 2, 5, 21].map((n) => ru.plural('room', n))).toEqual([
      '1 комната', '2 комнаты', '5 комнат', '21 комната',
    ]);
    expect(ru.t('sessionMapSetHeading', { session: 'MUD north' })).toBe('Набор карт для MUD north');
    expect(ru.t('formatReference', { reference: 'builtin:diku-en' })).toBe('builtin:diku-en');
  });

  it('uses the genitive Russian line forms required after "из" in replay summaries', () => {
    const ru = createSettingsMapperI18n('ru');
    expect([1, 2, 5].map((n) => ru.plural('lineAfterFrom', n))).toEqual([
      '1 строки', '2 строк', '5 строк',
    ]);
    expect(ru.t('replaySummary', {
      rooms: ru.plural('room', 1),
      lines: ru.plural('lineAfterFrom', 1),
      candidates: ru.plural('exitsCandidate', 1),
    })).toBe('1 комната из 1 строки; 1 строка-кандидат с выходами.');
  });

  it('keeps the Russian catalog explicit and compile-time complete', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/settings/mapperI18n.ts'), 'utf8');
    expect(source).not.toMatch(/const ru\s*=\s*\{[\s\S]*?\.\.\.en/);
    expect(source).toMatch(/const ru\s*=\s*\{[\s\S]*?\}\s+as const satisfies Catalog;/);
  });

  it('keeps Cyrillic mapper copy in the catalog, except raw MUD commands and the global language option', () => {
    const files = [
      'MapSetsSection.svelte', 'mapSets.ts', 'MapperProfilesSection.svelte',
      'mapperProfileForm.ts', 'SessionsSection.svelte', '../SettingsApp.svelte',
    ];
    for (const file of files) {
      const source = readFileSync(resolve(process.cwd(), 'src/settings', file), 'utf8');
      const matches = source.match(/[А-Яа-яЁё][^\n'"<>{}]*/gu) ?? [];
      const unexpected = matches.map((s) => s.trim()).filter((s) => s && !s.startsWith('откр две') && s !== 'Русский');
      expect(unexpected, file).toEqual([]);
    }
  });

  it('routes English authored Settings mapper UI sinks through the catalog', () => {
    const rawValues: Readonly<Record<string, readonly string[]>> = {
      'MapperProfilesSection.svelte': [
        '[ Exits:', '^\\[ Exits:\\s*(.*?)\\s*\\]$', 'ansi_delimited', 'fixed_width',
        'last_before_stripped', 'min_run', 'none', 'open door {dir_en}', 'rmud',
        '{dir}', '{dir_en}', 'откр две {dir}',
      ],
      'SessionsSection.svelte': ['~map'],
    };
    for (const file of ['MapSetsSection.svelte', 'MapperProfilesSection.svelte', 'SessionsSection.svelte']) {
      const path = resolve(process.cwd(), 'src/settings', file);
      const source = readFileSync(path, 'utf8');
      const cutoff = file === 'SessionsSection.svelte' ? source.indexOf("mapperI18n.t('sessionMapSetHeading'") : 0;
      const ast = parse(source, { filename: path }) as unknown as { fragment?: { nodes: unknown[] }; html?: { children: unknown[] } };
      const literals = new Set<string>();
      const visit = (node: unknown): void => {
        if (!node || typeof node !== 'object') return;
        const item = node as Record<string, unknown>;
        if (item.type === 'CallExpression') {
          const callee = item.callee as Record<string, unknown> | undefined;
          const property = callee?.property as Record<string, unknown> | undefined;
          if (callee?.type === 'MemberExpression' && (property?.name === 't' || property?.name === 'plural')) {
            (item.arguments as unknown[] ?? []).slice(1).forEach(visit);
            return;
          }
        }
        if (item.type === 'Literal' && typeof item.value === 'string' && Number(item.start) >= cutoff) {
          const value = item.value.trim();
          if (/[A-Za-z]{2}/.test(value)) literals.add(value);
          return;
        }
        if (item.type === 'TemplateElement' && Number(item.start) >= cutoff) {
          const value = String((item.value as { raw?: string } | undefined)?.raw ?? '').trim();
          if (/[A-Za-z]{2}/.test(value)) literals.add(value);
          return;
        }
        if (item.type === 'Text' && typeof item.data === 'string' && Number(item.start) >= cutoff) {
          const value = item.data.trim();
          if (/[A-Za-z]{2}/.test(value)) literals.add(value);
        }
        if (item.type === 'Attribute') {
          if (Number(item.start) >= cutoff && ['aria-label', 'title', 'placeholder'].includes(String(item.name))) {
            for (const valueNode of (item.value as unknown[] ?? [])) visit(valueNode);
          }
          return;
        }
        for (const [key, value] of Object.entries(item)) {
          if (['parent', 'metadata', 'attributes'].includes(key)) continue;
          if (Array.isArray(value)) value.forEach(visit);
          else if (value && typeof value === 'object') visit(value);
        }
        if (Array.isArray(item.attributes)) item.attributes.forEach(visit);
      };
      (ast.fragment?.nodes ?? ast.html?.children ?? []).forEach(visit);
      expect([...literals].sort(), file).toEqual([...(rawValues[file] ?? [])].sort());
    }

    const appPath = resolve(process.cwd(), 'src/SettingsApp.svelte');
    const appSource = readFileSync(appPath, 'utf8');
    const script = appSource.slice(appSource.indexOf('// --- Room formats'), appSource.indexOf('function historyKindLabel'));
    const tree = ts.createSourceFile(appPath, script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const sinkLiterals: string[] = [];
    const collectExpression = (node: ts.Node): void => {
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
        (node.expression.name.text === 't' || node.expression.name.text === 'plural')) {
        node.arguments.slice(1).forEach(collectExpression);
        return;
      }
      if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) ||
        ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) &&
        /[A-Za-z]{2}/.test(node.text)) sinkLiterals.push(node.text);
      ts.forEachChild(node, collectExpression);
    };
    const visitScript = (node: ts.Node): void => {
      const isSink = (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'confirm') ||
        (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
          ts.isIdentifier(node.left) && node.left.text === 'mapperProfileError');
      if (isSink) {
        const expression = ts.isCallExpression(node) ? node.arguments[0] : node.right;
        if (expression) collectExpression(expression);
      }
      ts.forEachChild(node, visitScript);
    };
    visitScript(tree);
    expect(sinkLiterals).toEqual([]);
    expect(appSource).toContain("{ id: 'room_formats', label: mapperI18n.t('tabRoomFormats') }");
    expect(appSource).toContain("{ id: 'map_sets', label: mapperI18n.t('tabMapSets') }");
  });

  it('audits TypeScript display helpers while allowing only stable protocol values', () => {
    const collectStrings = (node: ts.Node, result: Set<string>): void => {
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
        (node.expression.name.text === 't' || node.expression.name.text === 'plural')) {
        node.arguments.slice(1).forEach((argument) => collectStrings(argument, result));
        return;
      }
      if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) ||
        ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) && /[A-Za-z]{2}/.test(node.text)) {
        result.add(node.text);
      }
      ts.forEachChild(node, (child) => collectStrings(child, result));
    };

    const mapSetsPath = resolve(process.cwd(), 'src/settings/mapSets.ts');
    const mapSetsTree = ts.createSourceFile(mapSetsPath, readFileSync(mapSetsPath, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const mapSetValues = new Set<string>();
    mapSetsTree.forEachChild((node) => {
      if (ts.isFunctionDeclaration(node) && ['mapArchiveFileError', 'importFailureMessage', 'formatMapImportConflict'].includes(node.name?.text ?? '')) {
        collectStrings(node, mapSetValues);
      }
    });
    expect([...mapSetValues].sort()).toEqual([
      '.zip', 'duplicate_room', 'duplicate_zone', 'error', 'object', 'string',
    ].sort());

    const profilePath = resolve(process.cwd(), 'src/settings/mapperProfileForm.ts');
    const profileTree = ts.createSourceFile(profilePath, readFileSync(profilePath, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const provenanceValues = new Set<string>();
    profileTree.forEachChild((node) => {
      if (ts.isFunctionDeclaration(node) && ['mapperProfileLabel', 'mapperProvenanceLabel', 'mapperProvenanceNote'].includes(node.name?.text ?? '')) {
        collectStrings(node, provenanceValues);
      }
    });
    expect([...provenanceValues].sort()).toEqual([
      'Contributed', 'Starter', 'User', 'Verified',
    ].sort());
  });
});
