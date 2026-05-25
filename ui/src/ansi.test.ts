import { describe, expect, it } from 'vitest';
import { AnsiUp } from 'ansi_up';

import { normalizeAnsiTheme, renderAnsiHtml } from './ansi';

describe('ANSI rendering', () => {
  it('normalizes supported ANSI themes', () => {
    expect(normalizeAnsiTheme('classic')).toBe('classic');
    expect(normalizeAnsiTheme('high-contrast')).toBe('high-contrast');
    expect(normalizeAnsiTheme('tango-dark')).toBe('tango-dark');
    expect(normalizeAnsiTheme('dracula')).toBe('dracula');
    expect(normalizeAnsiTheme('gruvbox-dark')).toBe('gruvbox-dark');
    expect(normalizeAnsiTheme('unknown')).toBe('classic');
    expect(normalizeAnsiTheme(undefined)).toBe('classic');
  });

  it('keeps color state across entries until reset arrives', () => {
    const ansiUp = new AnsiUp();
    ansiUp.use_classes = true;

    const first = ansiUp.ansi_to_html('\x1b[31mfirst');
    const second = ansiUp.ansi_to_html('second');
    const reset = ansiUp.ansi_to_html('\x1b[0m');
    const third = ansiUp.ansi_to_html('third');

    expect(first).toContain('ansi-red-fg');
    expect(second).toContain('ansi-red-fg');
    expect(reset).toBe('');
    expect(third).not.toContain('ansi-red-fg');
    expect(second).toContain('second');
  });

  it('keeps classic theme ANSI classes unchanged', () => {
    const ansiUp = new AnsiUp();
    ansiUp.use_classes = true;

    const normal = renderAnsiHtml(ansiUp, '\x1b[31mnormal\x1b[0m', 'classic');
    const boldNormal = renderAnsiHtml(ansiUp, '\x1b[1;31mbold normal\x1b[0m', 'classic');

    expect(normal).toContain('ansi-red-fg');
    expect(normal).not.toContain('ansi-bright-red-fg');
    expect(boldNormal).toContain('ansi-red-fg');
    expect(boldNormal).not.toContain('ansi-bright-red-fg');
    expect(boldNormal).toContain('font-weight:bold');
    expect(boldNormal).toContain('bold normal');
  });

  it('distinguishes normal, bright, and MUD-style bold-normal foreground colors in high contrast', () => {
    const ansiUp = new AnsiUp();
    ansiUp.use_classes = true;

    const normal = renderAnsiHtml(ansiUp, '\x1b[31mnormal\x1b[0m', 'high-contrast');
    const bright = renderAnsiHtml(ansiUp, '\x1b[91mbright\x1b[0m', 'high-contrast');
    const boldNormal = renderAnsiHtml(ansiUp, '\x1b[1;31mbold normal\x1b[0m', 'high-contrast');

    expect(normal).toContain('ansi-red-fg');
    expect(normal).not.toContain('ansi-bright-red-fg');
    expect(bright).toContain('ansi-bright-red-fg');
    expect(boldNormal).toContain('ansi-bright-red-fg');
    expect(boldNormal).not.toContain('ansi-red-fg');
    expect(boldNormal).toContain('font-weight:bold');
    expect(boldNormal).toContain('bold normal');
  });

  it('promotes all bold normal MUD foreground colors to bright classes in high contrast', () => {
    const pairs: Array<[string, string, string]> = [
      ['30', 'ansi-black-fg', 'ansi-bright-black-fg'],
      ['31', 'ansi-red-fg', 'ansi-bright-red-fg'],
      ['32', 'ansi-green-fg', 'ansi-bright-green-fg'],
      ['33', 'ansi-yellow-fg', 'ansi-bright-yellow-fg'],
      ['34', 'ansi-blue-fg', 'ansi-bright-blue-fg'],
      ['35', 'ansi-magenta-fg', 'ansi-bright-magenta-fg'],
      ['36', 'ansi-cyan-fg', 'ansi-bright-cyan-fg'],
      ['37', 'ansi-white-fg', 'ansi-bright-white-fg'],
    ];

    for (const [code, normalClass, brightClass] of pairs) {
      const ansiUp = new AnsiUp();
      ansiUp.use_classes = true;
      const html = renderAnsiHtml(ansiUp, `\x1b[1;${code}mtext\x1b[0m`, 'high-contrast');

      expect(html).toContain(brightClass);
      expect(html).not.toContain(normalClass);
      expect(html).toContain('font-weight:bold');
      expect(html).toContain('text');
    }
  });

  it('keeps restored outer color after highlight-like reset+restore sequence', () => {
    const ansiUp = new AnsiUp();
    ansiUp.use_classes = true;

    const first = ansiUp.ansi_to_html('\x1b[34mdanger\x1b[0m\x1b[31m zone');
    const second = ansiUp.ansi_to_html('plain next');
    const third = ansiUp.ansi_to_html('\x1b[0m');
    const fourth = ansiUp.ansi_to_html('after reset');

    expect(first).toContain('ansi-blue-fg');
    expect(first).toContain('ansi-red-fg');
    expect(second).toContain('ansi-red-fg');
    expect(third).toBe('');
    expect(fourth).not.toContain('ansi-red-fg');
    expect(second).toContain('plain next');
  });
});
