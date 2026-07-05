export const fontSizeStorageKey = 'mudhost.fontSizePx';
export const defaultFontSize = 16;
export const minFontSize = 4;
export const maxFontSize = 24;
export const lineHeightRatio = 1.4;

export function clampFontSize(value: number): number {
  return Math.max(minFontSize, Math.min(maxFontSize, Math.round(value)));
}

// Line-height must stay a whole number of pixels: glyphs paint on whole
// physical pixels, so a fractional line-height (16px × 1.4 = 22.4px) makes
// the gap between adjacent output lines alternate by one pixel (issue #4).
export function lineHeightFor(fontSize: number): number {
  return Math.round(clampFontSize(fontSize) * lineHeightRatio);
}

export function applyFontSizeVars(root: HTMLElement, value: number): number {
  const next = clampFontSize(value);
  root.style.setProperty('--app-font-size', `${next}px`);
  root.style.setProperty('--app-line-height', `${lineHeightFor(next)}px`);
  return next;
}
