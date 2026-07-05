import { describe, expect, it } from 'vitest';

import { applyFontSizeVars, lineHeightRatio, maxFontSize, minFontSize } from './fontSize';

describe('font size CSS variables', () => {
  // Glyphs paint on whole physical pixels, so a fractional line-height
  // (16px × 1.4 = 22.4px) makes the gap between adjacent output lines
  // alternate by one pixel (GitHub issue #4).
  it('sets an integer pixel line-height for every allowed font size', () => {
    for (let size = minFontSize; size <= maxFontSize; size++) {
      const root = document.createElement('div');
      applyFontSizeVars(root, size);

      expect(root.style.getPropertyValue('--app-font-size')).toBe(`${size}px`);

      const lineHeight = root.style.getPropertyValue('--app-line-height');
      expect(lineHeight).toMatch(/^\d+px$/);

      const px = Number(lineHeight.replace('px', ''));
      expect(Math.abs(px - size * lineHeightRatio)).toBeLessThanOrEqual(0.5);
    }
  });

  it('clamps out-of-range sizes and returns the applied value', () => {
    const root = document.createElement('div');

    expect(applyFontSizeVars(root, 1)).toBe(minFontSize);
    expect(root.style.getPropertyValue('--app-font-size')).toBe(`${minFontSize}px`);

    expect(applyFontSizeVars(root, 99)).toBe(maxFontSize);
    expect(root.style.getPropertyValue('--app-font-size')).toBe(`${maxFontSize}px`);

    expect(applyFontSizeVars(root, 16.4)).toBe(16);
  });
});
