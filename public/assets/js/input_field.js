class InputField {
  constructor(history) {
    // Attach event handlers to the input field
    document.getElementById('input-text').addEventListener('keydown', async function (event) {
      const inputField = event.target;
      const currentValue = inputField.value;

      // 1. On Enter, clear text and run SendText(value)
      if (event.key === 'Enter') {
        const value = inputField.value;
        inputField.value = ''; // Clear the input field
        SendText(value); // Send the text
      }

      // 2. On Arrow Up, update value from history
      if (event.key === 'ArrowUp') {
        inputField.value = history.up(currentValue); // Update with the previous history value

        const length = inputField.value.length;
        inputField.setSelectionRange(length, length);
        event.preventDefault()
      }

      // 3. On Arrow Down, update value from history
      if (event.key === 'ArrowDown') {
        inputField.value = history.down(currentValue); // Update with the next history value
        const length = inputField.value.length;
        inputField.setSelectionRange(length, length);
        event.preventDefault()
      }

      // 4. On Ctrl+R, search history
      if (event.ctrlKey && event.key === 'r') {
        isHistorySearch = true
        inputField.value = await searchHistory(currentValue); // Perform search in history
        const length = inputField.value.length;
        inputField.setSelectionRange(length, length);
        event.preventDefault()
      }
    });
  }
}
