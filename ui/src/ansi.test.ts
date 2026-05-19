import { describe, expect, it } from 'vitest';
import { AnsiUp } from 'ansi_up';

describe('AnsiUp streaming behavior', () => {
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
