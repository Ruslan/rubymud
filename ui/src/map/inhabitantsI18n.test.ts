import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

import {
  createInhabitantsI18n,
  inhabitantsCatalogKeys,
  inhabitantsPlaceholders,
  inhabitantsPluralKeys,
  inhabitantsPluralPlaceholders,
} from './inhabitantsI18n';

describe('Knowledge inhabitants i18n', () => {
  it('keeps explicit en/ru catalogs, placeholders, and plural forms complete', () => {
    expect(inhabitantsCatalogKeys('ru')).toEqual(inhabitantsCatalogKeys('en'));
    for (const key of inhabitantsCatalogKeys('en')) {
      expect(inhabitantsPlaceholders('ru', key)).toEqual(inhabitantsPlaceholders('en', key));
    }
    expect(inhabitantsPluralKeys('ru')).toEqual(inhabitantsPluralKeys('en'));
    for (const key of inhabitantsPluralKeys('en')) {
      expect(inhabitantsPluralPlaceholders('ru', key)).toEqual(inhabitantsPluralPlaceholders('en', key));
    }
    const source = readFileSync(resolve(process.cwd(), 'src/map/inhabitantsI18n.ts'), 'utf8');
    expect(source).not.toMatch(/const ru\s*=\s*\{[\s\S]*?\.\.\.en/);
    expect(source).toMatch(/const ru\s*=\s*\{[\s\S]*?\}\s+as const satisfies Catalog;/);
  });

  it('localizes copy and placement counts while preserving interpolated game data', () => {
    const ru = createInhabitantsI18n('ru');
    expect(ru.t('roomLine', { room: 'Кузница', zone: 'Город', x: 3, y: 4, l: 0 }))
      .toBe('Кузница · Город (3,4,0)');
    expect([1, 2, 5].map((n) => ru.plural('placement', n))).toEqual([
      '1 размещение', '2 размещения', '5 размещений',
    ]);
    expect(ru.t('placeAria', { name: 'Странствующий лекарь' })).toBe('Разместить: Странствующий лекарь');
  });

  it('routes English authored inhabitants UI sinks through the catalog', () => {
    const path = resolve(process.cwd(), 'src/map/inhabitants.ts');
    const source = readFileSync(path, 'utf8');
    const tree = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const result: string[] = [];
    const collect = (node: ts.Node): void => {
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
        (node.expression.name.text === 't' || node.expression.name.text === 'plural')) {
        node.arguments.slice(1).forEach(collect);
        return;
      }
      if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) ||
        ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) &&
        /[A-Za-z]{2}/.test(node.text)) result.push(node.text.trim());
      ts.forEachChild(node, collect);
    };
    const visit = (node: ts.Node): void => {
      if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
        ts.isPropertyAccessExpression(node.left) &&
        ['textContent', 'placeholder', 'title'].includes(node.left.name.text)) collect(node.right);
      if (ts.isCallExpression(node)) {
        const name = ts.isIdentifier(node.expression) ? node.expression.text
          : ts.isPropertyAccessExpression(node.expression) ? node.expression.name.text : '';
        if (name === 'labelWrap' && node.arguments[0]) collect(node.arguments[0]);
        if (name === 'setMessage' && node.arguments[0]) collect(node.arguments[0]);
        if (name === 'confirm' && node.arguments[0]) collect(node.arguments[0]);
        if (name === 'setAttribute' && ts.isStringLiteral(node.arguments[0]) &&
          ['aria-label', 'title'].includes(node.arguments[0].text) && node.arguments[1]) collect(node.arguments[1]);
      }
      ts.forEachChild(node, visit);
    };
    visit(tree);
    expect([...new Set(result)].sort()).toEqual([]);
  });
});
