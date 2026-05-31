type NavigationSession = {
  active: boolean;
  query: string;
  index: number;
};

export class InputHistory {
  private history: string[] = [];
  private navigation: NavigationSession = {
    active: false,
    query: '',
    index: 0,
  };

  private startsWithQuery(value: string, query: string): boolean {
    return value.toLocaleLowerCase().startsWith(query.toLocaleLowerCase());
  }

  constructor() {
    this.loadHistory();
    this.resetNavigation();
  }

  resetNavigation() {
    this.navigation = {
      active: false,
      query: '',
      index: this.history.length,
    };
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
    this.resetNavigation();
    return query;
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
