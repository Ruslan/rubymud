import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

const files = ['pane.ts', 'cellMenu.ts', 'edgeEditor.ts', 'zoneCrud.ts', '../render.ts'] as const;
const parserAllowlist: Readonly<Record<string, readonly string[]>> = {
  'cellMenu.ts': [
    '/[а-яёa-z0-9]+/g', 'в', 'вв', 'вн', 'вверх', 'вниз', 'восток',
    'з', 'запад', 'с', 'север', 'ю', 'юг',
  ],
  'edgeEditor.ts': ['вверх', 'вниз', 'восток', 'запад', 'север', 'юг'],
};
// These are terminal-shell controls owned by render.ts rather than mapper copy.
// Keeping the list exact means any newly-authored literal still fails the audit.
const authoredSinkAllowlist: Readonly<Record<string, readonly string[]>> = {
  '../render.ts': [
    'Clear override', 'Close', 'Close search', 'Newer match',
    'No groups defined in active profiles.', 'No variables defined yet.',
    'Older match', 'Reconnect', 'Scroll to bottom', 'Search buffer',
    'Search this buffer', 'Split Down', 'Split Right',
    'Toggle scrollback split', 'tick', 'tick off',
  ],
};

function cyrillicRuntimeTokens(filename: string): string[] {
  const path = resolve(process.cwd(), 'src/map', filename);
  const source = readFileSync(path, 'utf8');
  const tree = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const result = new Set<string>();
  const visit = (node: ts.Node): void => {
    if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) ||
      ts.isRegularExpressionLiteral(node) || ts.isIdentifier(node)) && /[А-Яа-яЁё]/u.test(node.getText(tree))) {
      const text = ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)
        ? node.text : node.getText(tree);
      result.add(text);
    }
    ts.forEachChild(node, visit);
  };
  visit(tree);
  return [...result].sort();
}

function englishAuthoredSinkLiterals(filename: string): string[] {
  const path = resolve(process.cwd(), 'src/map', filename);
  const source = readFileSync(path, 'utf8');
  const tree = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const result: string[] = [];
  const collect = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
      (node.expression.name.text === 't' || node.expression.name.text === 'plural')) {
      node.arguments.slice(1).forEach(collect);
      return;
    }
    const comparisonOperand = ts.isStringLiteral(node) && ts.isBinaryExpression(node.parent) &&
      [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken,
        ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken]
        .includes(node.parent.operatorToken.kind);
    const htmlEntity = 'text' in node && /^&[a-z]+;$/i.test(String(node.text));
    if (!comparisonOperand && !htmlEntity && (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) ||
      ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) &&
      /[A-Za-z]{2}/.test(node.text)) result.push(node.text.trim());
    ts.forEachChild(node, collect);
  };
  const visit = (node: ts.Node): void => {
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
      ts.isPropertyAccessExpression(node.left) &&
      ['textContent', 'innerHTML', 'title', 'placeholder'].includes(node.left.name.text)) collect(node.right);
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)) {
      const method = node.expression.name.text;
      if (method === 'setAttribute' && ts.isStringLiteral(node.arguments[0]) &&
        node.arguments[0].text === 'aria-label' && node.arguments[1]) collect(node.arguments[1]);
      if (['setStatus', 'menuButton', 'confirm', 'setCustomValidity'].includes(method) && node.arguments[0])
        collect(node.arguments[0]);
      if (method === 'iconButton' && node.arguments[1]) collect(node.arguments[1]);
    }
    ts.forEachChild(node, visit);
  };
  visit(tree);
  return [...new Set(result)].sort();
}

describe('mapper authored-copy extraction', () => {
  it('leaves Cyrillic runtime literals only in the explicit MUD direction parser allowlist', () => {
    for (const file of files) {
      expect(cyrillicRuntimeTokens(file), file)
        .toEqual([...(parserAllowlist[file] ?? [])].sort());
    }
  });

  it('routes English authored strings at UI sinks through the catalog', () => {
    for (const file of files) {
      expect(englishAuthoredSinkLiterals(file), file)
        .toEqual([...(authoredSinkAllowlist[file] ?? [])].sort());
    }
  });

  it('keeps the Russian catalog explicit and compile-time complete', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/map/i18n.ts'), 'utf8');
    expect(source).not.toMatch(/const ru\s*=\s*\{[\s\S]*?\.\.\.en/);
    expect(source).toMatch(/const ru\s*=\s*\{[\s\S]*?\}\s+as const satisfies Catalog;/);
  });
});
