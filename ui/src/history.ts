type NavigationSession = {
  active: boolean;
  query: string;
  index: number;
};

type SearchSession = {
  active: boolean;
  query: string;
  draft: string;
  index: number;
};

export class InputHistory {
  private history: string[] = [];
  private navigation: NavigationSession = {
    active: false,
    query: '',
    index: 0,
  };
  private search: SearchSession = {
    active: false,
    query: '',
    draft: '',
    index: 0,
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
    this.resetSearch();
  }

  resetSearch() {
    this.search = {
      active: false,
      query: '',
      draft: '',
      index: this.history.length,
    };
  }

  isSearchActive(): boolean {
    return this.search.active;
  }

  push(value: string) {
    if (!value) return;
    this.history.push(value);
    this.resetNavigation();
    this.saveHistory();
  }

  merge(remoteHistory: string[]) {
    this.history = [...this.history, ...(remoteHistory || [])].filter((value, index, self) => {
      return self.indexOf(value) === index;
    });
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

  reverseSearch(currentValue: string): string {
    this.ensureSearch(currentValue);

    for (let i = this.search.index - 1; i >= 0; i--) {
      const value = this.history[i];
      if (value && this.containsQuery(value, this.search.query)) {
        this.search.index = i;
        return value;
      }
    }

    return this.currentSearchValue();
  }

  acceptSearch(): string | null {
    if (!this.search.active) {
      return null;
    }

    const value = this.currentSearchValue();
    this.resetNavigation();
    return value;
  }

  cancelSearch(): string | null {
    if (!this.search.active) {
      return null;
    }

    const draft = this.search.draft;
    this.resetNavigation();
    return draft;
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

  private ensureSearch(currentValue: string) {
    if (this.search.active) {
      return;
    }

    this.resetArrowNavigation();
    this.search = {
      active: true,
      query: currentValue,
      draft: currentValue,
      index: this.history.length,
    };
  }

  private currentNavigationValue(): string {
    const current = this.history[this.navigation.index];
    return current && this.startsWithQuery(current, this.navigation.query) ? current : this.navigation.query;
  }

  private currentSearchValue(): string {
    const current = this.history[this.search.index];
    return current && this.containsQuery(current, this.search.query) ? current : this.search.draft;
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
