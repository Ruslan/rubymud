type NavigationSession = {
  active: boolean;
  query: string;
  index: number;
};

type ReverseSearchSession = {
  active: boolean;
  query: string;
  draft: string;
  index: number;
  match: string | null;
};

export type ReverseSearchState = {
  active: boolean;
  query: string;
  draft: string;
  match: string | null;
};

export const commandHistoryStorageKey = 'commandHistory';

export class InputHistory {
  private history: string[] = [];
  private pendingLocalBeforeRemoteRestore: string[] = [];
  private hasMergedRemoteHistory = false;
  private navigation: NavigationSession = {
    active: false,
    query: '',
    index: 0,
  };
  private reverseSearchSession: ReverseSearchSession = {
    active: false,
    query: '',
    draft: '',
    index: 0,
    match: null,
  };

  private startsWithQuery(value: string, query: string): boolean {
    return value.toLocaleLowerCase().startsWith(query.toLocaleLowerCase());
  }

  private containsQuery(value: string, query: string): boolean {
    return value.toLocaleLowerCase().includes(query.toLocaleLowerCase());
  }

  constructor() {
    this.loadHistory();
    this.resetNavigation();
  }

  resetNavigation() {
    this.resetArrowNavigation();
    this.resetReverseSearch();
  }

  resetReverseSearch() {
    this.reverseSearchSession = {
      active: false,
      query: '',
      draft: '',
      index: this.history.length,
      match: null,
    };
  }

  resetSearch() {
    this.resetReverseSearch();
  }

  isReverseSearchActive(): boolean {
    return this.reverseSearchSession.active;
  }

  isSearchActive(): boolean {
    return this.isReverseSearchActive();
  }

  push(value: string) {
    if (!value) return;
    this.upsert(value);
    if (!this.hasMergedRemoteHistory) {
      this.pendingLocalBeforeRemoteRestore = this.upsertIn(this.pendingLocalBeforeRemoteRestore, value);
    }
    this.resetNavigation();
    this.saveHistory();
  }

  merge(remoteHistory: string[]) {
    const navigationValue = this.activeNavigationValue();
    const localHistory = [...this.history];
    const pendingLocal = this.hasMergedRemoteHistory ? [] : [...this.pendingLocalBeforeRemoteRestore];
    this.history = [];
    this.mergeValues(localHistory);
    this.mergeValues(remoteHistory || []);
    this.mergeValues(pendingLocal);
    this.pendingLocalBeforeRemoteRestore = [];
    this.hasMergedRemoteHistory = true;
    this.reanchorNavigation(navigationValue);
    this.resetReverseSearch();
    this.saveHistory();
  }

  syncFromStorage(): boolean {
    const navigationValue = this.activeNavigationValue();
    const storedHistory = this.readStoredHistory();
    const previous = [...this.history];
    const pendingLocal = this.hasMergedRemoteHistory ? [] : [...this.pendingLocalBeforeRemoteRestore];
    this.history = [];
    this.mergeValues(previous);
    this.mergeValues(storedHistory);
    this.mergeValues(pendingLocal);
    const changed = !this.historiesEqual(previous, this.history);
    if (changed) {
      this.reanchorNavigation(navigationValue);
      this.resetReverseSearch();
    }
    return changed;
  }

  up(currentValue: string): string {
    this.ensureNavigation(currentValue);

    for (let i = this.navigation.index - 1; i >= 0; i--) {
      const value = this.history[i];
      if (value && this.startsWithQuery(value, this.navigation.query)) {
        this.navigation.index = i;
        return value;
      }
    }

    return this.currentNavigationValue();
  }

  down(currentValue: string): string {
    this.ensureNavigation(currentValue);

    for (let i = this.navigation.index + 1; i < this.history.length; i++) {
      const value = this.history[i];
      if (value && this.startsWithQuery(value, this.navigation.query)) {
        this.navigation.index = i;
        return value;
      }
    }

    const query = this.navigation.query;
    this.resetArrowNavigation();
    return query;
  }

  beginReverseSearch(draft: string): ReverseSearchState {
    this.resetArrowNavigation();
    this.reverseSearchSession = {
      active: true,
      query: '',
      draft,
      index: this.history.length,
      match: null,
    };
    return this.getReverseSearchState();
  }

  updateReverseSearchQuery(query: string): ReverseSearchState {
    this.ensureReverseSearch('');
    this.reverseSearchSession.query = query;
    this.reverseSearchSession.index = this.history.length;
    const match = this.findOlderReverseMatch(this.history.length, query);
    this.reverseSearchSession.match = match.value;
    this.reverseSearchSession.index = match.index;
    return this.getReverseSearchState();
  }

  advanceReverseSearch(): ReverseSearchState {
    this.ensureReverseSearch('');
    if (!this.reverseSearchSession.query) {
      this.reverseSearchSession.match = null;
      this.reverseSearchSession.index = this.history.length;
      return this.getReverseSearchState();
    }

    const next = this.findOlderReverseMatch(this.reverseSearchSession.index, this.reverseSearchSession.query);
    if (next.value !== null) {
      this.reverseSearchSession.match = next.value;
      this.reverseSearchSession.index = next.index;
    }
    return this.getReverseSearchState();
  }

  acceptReverseSearch(): string | null {
    if (!this.reverseSearchSession.active) {
      return null;
    }

    const value = this.reverseSearchSession.match;
    this.resetNavigation();
    return value;
  }

  cancelReverseSearch(): string {
    const draft = this.reverseSearchSession.draft;
    this.resetNavigation();
    return draft;
  }

  getReverseSearchState(): ReverseSearchState {
    return {
      active: this.reverseSearchSession.active,
      query: this.reverseSearchSession.query,
      draft: this.reverseSearchSession.draft,
      match: this.reverseSearchSession.match,
    };
  }

  reverseSearchCommand(currentValue: string): string | null {
    if (!this.reverseSearchSession.active) {
      this.beginReverseSearch(currentValue);
      this.updateReverseSearchQuery(currentValue);
    } else {
      this.advanceReverseSearch();
    }
    return this.reverseSearchSession.match;
  }

  reverseSearch(currentValue: string): string | null {
    return this.reverseSearchCommand(currentValue);
  }

  acceptSearch(): string | null {
    return this.acceptReverseSearch();
  }

  cancelSearch(): string | null {
    if (!this.reverseSearchSession.active) {
      return null;
    }
    return this.cancelReverseSearch();
  }

  matches(prefix: string, limit = 3): string[] {
    if (!prefix) {
      return [];
    }

    const seen = new Set<string>();
    const result: string[] = [];

    for (let i = this.history.length - 1; i >= 0 && result.length < limit; i--) {
      const value = this.history[i];
      if (!value || !this.startsWithQuery(value, prefix) || seen.has(value)) {
        continue;
      }

      seen.add(value);
      result.push(value);
    }

    return result;
  }

  private resetArrowNavigation() {
    this.navigation = {
      active: false,
      query: '',
      index: this.history.length,
    };
  }

  private activeNavigationValue(): string | undefined {
    if (!this.navigation.active) return undefined;
    return this.history[this.navigation.index];
  }

  // Re-anchor an active arrow-navigation session after the history array was
  // rebuilt (backend restore merge on reconnect, cross-tab storage sync). A
  // blanket reset here would make the next arrow press adopt the recalled
  // command from the input as a new prefix filter, so up/down would suddenly
  // skip to unrelated entries (GitHub issue #3).
  private reanchorNavigation(previousValue: string | undefined) {
    if (!this.navigation.active) {
      this.resetArrowNavigation();
      return;
    }
    if (previousValue === undefined) {
      // The session was anchored at the draft position past the newest entry.
      this.navigation.index = this.history.length;
      return;
    }
    const index = this.history.lastIndexOf(previousValue);
    if (index === -1) {
      this.resetArrowNavigation();
      return;
    }
    this.navigation.index = index;
  }

  private ensureNavigation(currentValue: string) {
    if (this.navigation.active) {
      return;
    }

    this.navigation = {
      active: true,
      query: currentValue,
      index: this.history.length,
    };
  }

  private ensureReverseSearch(draft: string) {
    if (this.reverseSearchSession.active) {
      return;
    }
    this.beginReverseSearch(draft);
  }

  private findOlderReverseMatch(startExclusive: number, query: string): { value: string | null; index: number } {
    if (!query) {
      return { value: null, index: this.history.length };
    }

    for (let i = startExclusive - 1; i >= 0; i--) {
      const value = this.history[i];
      if (value && this.containsQuery(value, query)) {
        return { value, index: i };
      }
    }
    return { value: null, index: startExclusive };
  }

  private currentNavigationValue(): string {
    const current = this.history[this.navigation.index];
    return current && this.startsWithQuery(current, this.navigation.query) ? current : this.navigation.query;
  }

  private loadHistory() {
    this.history = this.readStoredHistory();
  }

  private readStoredHistory(): string[] {
    const storedHistory = localStorage.getItem(commandHistoryStorageKey);
    if (!storedHistory) return [];

    try {
      const parsed = JSON.parse(storedHistory);
      return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === 'string' && value.length > 0) : [];
    } catch (e) {
      console.error('Failed to parse history', e);
      return [];
    }
  }

  private saveHistory() {
    localStorage.setItem(commandHistoryStorageKey, JSON.stringify(this.history));
  }

  private mergeValues(values: string[]) {
    for (const value of values) {
      if (!value) continue;
      this.upsert(value);
    }
  }

  private upsert(value: string) {
    this.history = this.upsertIn(this.history, value);
  }

  private upsertIn(values: string[], value: string): string[] {
    return [...values.filter((item) => item !== value), value];
  }

  private historiesEqual(left: string[], right: string[]): boolean {
    return left.length === right.length && left.every((value, index) => value === right[index]);
  }
}
