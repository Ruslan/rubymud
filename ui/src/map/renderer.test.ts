import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MapRenderer } from './renderer';

// jsdom's <canvas>.getContext('2d') returns null, so to exercise MapRenderer's
// listener lifecycle we hand it a canvas whose getContext returns a no-op 2d
// context stub. Only the members the renderer actually calls are stubbed.
function fakeCtx(): CanvasRenderingContext2D {
  const noop = () => {};
  return new Proxy(
    {
      setTransform: noop,
      clearRect: noop,
      beginPath: noop,
      moveTo: noop,
      lineTo: noop,
      arc: noop,
      arcTo: noop,
      closePath: noop,
      fill: noop,
      stroke: noop,
      fillText: noop,
      fillStyle: '',
      strokeStyle: '',
      lineWidth: 0,
      font: '',
      textAlign: '',
      textBaseline: '',
    },
    { get: (t, p) => (p in t ? (t as any)[p] : noop) },
  ) as unknown as CanvasRenderingContext2D;
}

function makeCanvas(): HTMLCanvasElement {
  const cv = document.createElement('canvas');
  const ctx = fakeCtx();
  vi.spyOn(cv, 'getContext').mockReturnValue(ctx as any);
  // clientWidth/Height are 0 in jsdom; give the renderer a viewport to work with.
  Object.defineProperty(cv, 'clientWidth', { value: 600, configurable: true });
  Object.defineProperty(cv, 'clientHeight', { value: 400, configurable: true });
  cv.getBoundingClientRect = () => ({ left: 0, top: 0, width: 600, height: 400, right: 600, bottom: 400, x: 0, y: 0, toJSON() {} });
  return cv;
}

describe('MapRenderer listener lifecycle', () => {
  let rafSpy: ReturnType<typeof vi.spyOn>;
  let rafQueue: FrameRequestCallback[];

  beforeEach(() => {
    // Deferred rAF: queue callbacks (like a real browser) so the render()
    // coalescing guard is observable. flushRaf() runs the queued paints.
    rafQueue = [];
    rafSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb: FrameRequestCallback) => {
      rafQueue.push(cb);
      return rafQueue.length;
    });
  });

  afterEach(() => {
    rafSpy.mockRestore();
    vi.restoreAllMocks();
  });

  function flushRaf() {
    const q = rafQueue;
    rafQueue = [];
    for (const cb of q) cb(0);
  }

  it('constructs with a 2d context and rAF-coalesces render()', () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    rafSpy.mockClear();
    // Two render() calls before the frame runs → the guard means only ONE rAF
    // is scheduled (they coalesce into a single paint).
    r.render();
    r.render();
    expect(rafSpy).toHaveBeenCalledTimes(1);
    // After the frame runs, a fresh render() schedules again.
    flushRaf();
    r.render();
    expect(rafSpy).toHaveBeenCalledTimes(2);
  });

  it('destroy() tears down window listeners so later global mouse moves do not schedule paints', () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    // Prime a drag so the window mousemove handler would otherwise call render().
    cv.dispatchEvent(new MouseEvent('mousedown', { clientX: 10, clientY: 10 }));

    r.destroy();
    rafSpy.mockClear();

    // A global mousemove after destroy() must not reach the (aborted) handler.
    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 50, clientY: 50 }));
    expect(rafSpy).not.toHaveBeenCalled();
  });

  it('render() is a no-op after destroy()', () => {
    const cv = makeCanvas();
    const r = new MapRenderer(cv);
    r.destroy();
    rafSpy.mockClear();
    r.render();
    expect(rafSpy).not.toHaveBeenCalled();
  });
});
