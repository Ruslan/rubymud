import { appendReverseSearchHintContent } from './historyHint';
import { eventToShortcut, normalizeShortcut } from './hotkeys';
import type { ReverseSearchState } from './history';

export type ReverseSearchSearcher = {
  beginReverseSearch(draft: string): ReverseSearchState;
  updateReverseSearchQuery(query: string): ReverseSearchState;
  advanceReverseSearch(): ReverseSearchState;
  acceptReverseSearch(): string | null;
  cancelReverseSearch(): string;
  resetReverseSearch(): void;
  isReverseSearchActive(): boolean;
  getReverseSearchState(): ReverseSearchState;
};

export type ReverseSearchControllerOptions = {
  input: HTMLInputElement;
  inputWrap: HTMLElement | null;
  searcher: ReverseSearchSearcher;
};

export function isReverseSearchShortcut(event: KeyboardEvent): boolean {
  return normalizeShortcut(eventToShortcut(event)) === 'ctrl+r';
}

function isPrintableSearchKey(event: KeyboardEvent): boolean {
  return event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey;
}

export class ReverseSearchController {
  private input: HTMLInputElement;
  private inputWrap: HTMLElement | null;
  private searcher: ReverseSearchSearcher;
  private hint: HTMLButtonElement;

  constructor(options: ReverseSearchControllerOptions) {
    this.input = options.input;
    this.inputWrap = options.inputWrap;
    this.searcher = options.searcher;
    this.hint = document.createElement('button');
    this.hint.type = 'button';
    this.hint.className = 'reverse-search-hint';
    this.hint.hidden = true;
    this.inputWrap?.appendChild(this.hint);
    this.hint.addEventListener('click', () => this.accept());
  }

  isActive(): boolean {
    return this.searcher.isReverseSearchActive();
  }

  handleGlobalKeydown(event: KeyboardEvent): boolean {
    if (!isReverseSearchShortcut(event)) {
      return false;
    }

    event.preventDefault();
    event.stopPropagation();
    this.startOrAdvance();
    return true;
  }

  handleInputKeydown(event: KeyboardEvent): boolean {
    if (isReverseSearchShortcut(event)) {
      event.preventDefault();
      event.stopPropagation();
      this.startOrAdvance();
      return true;
    }

    if (!this.isActive()) {
      return false;
    }

    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      this.cancel();
      return true;
    }

    if (event.key === 'Enter') {
      event.preventDefault();
      event.stopPropagation();
      this.accept();
      return true;
    }

    if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
      event.preventDefault();
      event.stopPropagation();
      this.cancel();
      return false;
    }

    if (event.key === 'Backspace' || event.key === 'Delete') {
      event.preventDefault();
      event.stopPropagation();
      const state = this.searcher.getReverseSearchState();
      this.render(this.searcher.updateReverseSearchQuery(state.query.slice(0, -1)));
      return true;
    }

    if (isPrintableSearchKey(event)) {
      event.preventDefault();
      event.stopPropagation();
      const state = this.searcher.getReverseSearchState();
      this.render(this.searcher.updateReverseSearchQuery(state.query + event.key));
      return true;
    }

    event.preventDefault();
    event.stopPropagation();
    return true;
  }

  reset() {
    this.searcher.resetReverseSearch();
    this.hide();
  }

  private startOrAdvance() {
    this.input.focus();
    const state = this.isActive()
      ? this.searcher.advanceReverseSearch()
      : this.searcher.beginReverseSearch(this.input.value);
    this.render(state);
  }

  private accept() {
    const value = this.searcher.acceptReverseSearch();
    if (value !== null) {
      this.input.value = value;
      this.setCaretToEnd();
    }
    this.hide();
  }

  private cancel() {
    this.input.value = this.searcher.cancelReverseSearch();
    this.setCaretToEnd();
    this.hide();
  }

  private render(state: ReverseSearchState) {
    appendReverseSearchHintContent(this.hint, state.query, state.match);
    this.hint.disabled = !state.match;
    this.hint.title = state.match ? `Accept history match: ${state.match}` : 'No history match';
    this.hint.hidden = false;
    this.inputWrap?.classList.add('input-wrap_reverse-search-active');
    this.positionHint();
  }

  private hide() {
    this.hint.hidden = true;
    this.hint.replaceChildren();
    this.inputWrap?.classList.remove('input-wrap_reverse-search-active');
  }

  private setCaretToEnd() {
    const length = this.input.value.length;
    this.input.setSelectionRange(length, length);
  }

  private measureInputTextWidth(value: string): number {
    const style = getComputedStyle(this.input);
    const probe = document.createElement('span');
    probe.textContent = value;
    probe.style.position = 'fixed';
    probe.style.visibility = 'hidden';
    probe.style.whiteSpace = 'pre';
    probe.style.font = style.font;
    document.body.appendChild(probe);
    const width = probe.getBoundingClientRect().width;
    probe.remove();
    return width;
  }

  private positionHint() {
    if (!this.inputWrap) return;
    const inputStyle = getComputedStyle(this.input);
    const paddingLeft = parseFloat(inputStyle.paddingLeft || '0');
    const paddingRight = parseFloat(inputStyle.paddingRight || '0');
    const textWidth = this.measureInputTextWidth(this.input.value);
    const maxLeft = Math.max(paddingLeft, this.input.clientWidth - paddingRight - 120);
    const left = Math.min(paddingLeft + textWidth - this.input.scrollLeft + 8, maxLeft);
    this.hint.style.left = `${Math.max(paddingLeft, left)}px`;
  }
}
