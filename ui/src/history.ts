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

export class InputHistory {
  private history: string[] = [];
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
    this.history = this.history.filter((item) => item !== value);
    this.history.push(value);
    this.resetNavigation();
    this.saveHistory();
  }

  merge(remoteHistory: string[]) {
    const localHistory = [...this.history];
    this.history = [];
    for (const value of localHistory) {
      if (!value) continue;
      this.history = this.history.filter((item) => item !== value);
      this.history.push(value);
    }
    for (const value of remoteHistory || []) {
      if (!value) continue;
      this.history = this.history.filter((item) => item !== value);
      this.history.push(value);
    }
    this.resetNavigation();
    this.saveHistory();
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
    const storedHistory = localStorage.getItem('commandHistory');
    if (!storedHistory) return;

    try {
      const parsed = JSON.parse(storedHistory);
      this.history = Array.isArray(parsed) ? parsed : [];
    } catch (e) {
      console.error('Failed to parse history', e);
    }
  }

  private saveHistory() {
    localStorage.setItem('commandHistory', JSON.stringify(this.history));
  }
}
