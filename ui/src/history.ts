export class InputHistory {
  private history: string[] = [];
  private historyIndex = -1;
  private historyViewed: string[] = [];
  private historyDirection: number | null = null;
  private lastResult: string | null = null;
  private arrowQuery = '';

  constructor() {
    this.loadHistory();
  }

  push(value: string) {
    if (!value) return;
    this.history.push(value);
    this.historyIndex = this.history.length;
    this.historyViewed = [];
    this.saveHistory();
  }

  merge(remoteHistory: string[]) {
    this.history = [...this.history, ...(remoteHistory || [])].filter((value, index, self) => {
      return self.indexOf(value) === index;
    });
    this.historyIndex = this.history.length;
    this.historyViewed = [];
    this.saveHistory();
  }

  up(currentValue: string): string {
    if (currentValue === this.lastResult) {
      currentValue = this.arrowQuery;
    } else {
      this.arrowQuery = currentValue;
    }

    if (this.historyDirection !== -1) {
      this.historyViewed = [];
      this.historyDirection = -1;
    }

    for (let i = this.historyIndex - 1; i >= 0; i--) {
      const value = this.history[i];
      if (value && value.startsWith(currentValue) && !this.historyViewed.includes(value)) {
        this.historyIndex = i;
        this.historyViewed.push(value);
        this.lastResult = value;
        return value;
      }
    }

    this.historyIndex = 0;
    return currentValue;
  }

  down(currentValue: string): string {
    if (currentValue === this.lastResult) {
      currentValue = this.arrowQuery;
    } else {
      this.arrowQuery = currentValue;
    }

    if (this.historyDirection !== 1) {
      this.historyViewed = [];
      this.historyDirection = 1;
    }

    for (let i = this.historyIndex + 1; i < this.history.length; i++) {
      const value = this.history[i];
      if (value && value.startsWith(currentValue) && !this.historyViewed.includes(value)) {
        this.historyIndex = i;
        this.historyViewed.push(value);
        this.lastResult = value;
        return value;
      }
    }

    this.historyIndex = this.history.length;
    return currentValue;
  }

  matches(prefix: string, limit = 3): string[] {
    if (!prefix) {
      return [];
    }

    const seen = new Set<string>();
    const result: string[] = [];

    for (let i = this.history.length - 1; i >= 0 && result.length < limit; i--) {
      const value = this.history[i];
      if (!value || !value.startsWith(prefix) || seen.has(value)) {
        continue;
      }

      seen.add(value);
      result.push(value);
    }

    return result;
  }

  private loadHistory() {
    const storedHistory = localStorage.getItem('commandHistory');
    if (!storedHistory) return;

    try {
      this.history = JSON.parse(storedHistory);
      this.historyIndex = this.history.length;
    } catch (e) {
      console.error('Failed to parse history', e);
    }
  }

  private saveHistory() {
    localStorage.setItem('commandHistory', JSON.stringify(this.history));
  }
}
