class InputHistory {
  constructor() {
    this.history = [];
    this.historyIndex = -1;
    this.loadHistory()

    this.historyValue = null
    this.historyViewed = []
    this.historySearch = false
    this.historyDirection = null
  }

  push(value) {
    this.history.push(value)
    this.historyIndex = this.history.length; // Reset history index after sending text
    this.historyViewed = []
    this.saveHistory()
  }

  saveHistory() {
    if (this.historyLoaded) {
      localStorage.setItem('commandHistory', JSON.stringify(this.history));
    }
  }

  merge(remoteHistory) {
    this.history = [...this.history, ...remoteHistory].filter((value, index, self) => {
      return self.indexOf(value) === index;
    });
    this.historyIndex = this.history.length;
    this.historyViewed = []
  }

  loadHistory() {
    const storedHistory = localStorage.getItem('commandHistory');
    if (storedHistory) {
      this.history = JSON.parse(storedHistory);
      this.historyIndex = this.history.length; // Set the index to the end of the restored history
    }
    this.historyLoaded = true
  }

  // Function to handle history up (ArrowUp)
  up(currentValue) {
    if (currentValue == this.lastResult) {
      currentValue = this.arrowQuery
    } else {
      this.arrowQuery = currentValue
    }

    if (this.historyDirection != -1) {
      this.historyViewed = []
      this.historyDirection = -1
    }

    // Start searching from the current history index
    for (let i = this.historyIndex - 1; i >= 0; i--) {
      let value = this.history[i]
      if (value && value.startsWith(currentValue) && !this.historyViewed.includes(value)) {
        this.historyIndex = i; // Update the history index
        this.historyViewed.push(value)
        this.lastResult = value
        return value; // Return the matched command
      }
    }
    this.historyIndex = 0; // Reset to the begin if no match is found
    return currentValue; // Return the current value if no match is found
  }

  // Function to handle history down (ArrowDown)
  down(currentValue) {
    if (currentValue == this.lastResult) {
      currentValue = this.arrowQuery
    } else {
      this.arrowQuery = currentValue
    }

    if (this.historyDirection != 1) {
      this.historyViewed = []
      this.historyDirection = 1
    }

    // Start searching from the current history index
    for (let i = this.historyIndex + 1; i < this.history.length; i++) {
      let value = this.history[i]
      if (value && value.startsWith(currentValue) && !this.historyViewed.includes(value)) {
        this.historyIndex = i; // Update the history index
        this.historyViewed.push(value)
        this.lastResult = value
        return value; // Return the matched command
      }
    }
    this.historyIndex = this.history.length; // Reset to the end if no match is found
    return currentValue; // Return the current value if no match is found
  }

  // Function to handle search in history (Ctrl+R)
  async search(currentValue) {
    // This is a placeholder, you can define your search behavior here
    // For now, let's just log the search attempt
    console.log('Searching history for:', currentValue);
    return currentValue; // You can modify this to return the search result
  }

  resetSearch(){
    this.historyValue = null
    this.historyIndex = history.length
    this.historySearch = false
    this.historyViewed = []
    this.historyDirection = null
  }
}
