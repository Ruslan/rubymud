class Buffers {
  constructor() {
    this.mainBuffer = document.getElementById('main_output')
    this.buffers = {};
    this.buffers['default'] = this.mainBuffer

    this.initSelector()
  }

  getBuffer(name) {
    if (!name) name = 'default'
    if (this.buffers[name]) return this.buffers[name]

    let currentBuffer = document.createElement('div')
    currentBuffer.className = 'output'
    currentBuffer.setAttribute("data-buffer", name)
    this.buffers[name] = currentBuffer

    this.updateSelectors()

    return currentBuffer
  }

  appendTo(name, div) {
    const currentOut = this.getBuffer(name)
    const shouldScroll = currentOut.parentNode && Math.abs(currentOut.parentNode.scrollTop - (currentOut.scrollHeight - currentOut.parentNode.offsetHeight)) < currentOut.parentNode.offsetHeight / 2;

    currentOut.append(div)

    // Check if the number of divs exceeds 2000
    if (currentOut.children.length > 2000) {
      // Remove the first 500 divs
      for (let i = 0; i < 500; i++) {
        currentOut.removeChild(currentOut.children[0]);
      }
    }

    if (shouldScroll) {
      this.scrollBufferBottom(name)
    }
  }

  scrollBufferBottom(name) {
    const currentOut = this.getBuffer(name)
    if (currentOut.parentNode) {
      currentOut.parentNode.scrollTop = currentOut.scrollHeight
    }
  }

  showWindow(select) {
    let wrap = select.closest(".section").querySelector('.output-wrapper')
    let currentOut = wrap.querySelector('.output')
    if (currentOut) wrap.removeChild(currentOut)
    if (select.value) {
      const name = select.value
      const buffer = this.getBuffer(name)
      if (buffer) {
        wrap.append(buffer)
        this.scrollBufferBottom(name)
      }
    }
  }

  initSelector() {
    document.querySelectorAll('.window-changer').forEach((e) => {
      e.addEventListener('change', (event) => {
        this.showWindow(event.target)

        if (event.target.value) {
          document.querySelectorAll('.window-changer').forEach((otherSelect) => {
            // Skip the current selector
            if (otherSelect !== event.target) {
              otherSelect.value = ''; // Set the value of all other selectors to empty
            }
          });
        }
      })
      this.showWindow(e)
    })
  }

  updateSelectors() {
    document.querySelectorAll('.window-changer').forEach((e) => {
      // Capture the currently selected value before clearing the options
      const selectedValue = e.value;

      // Clear current options
      e.innerHTML = '';

      // Add the '-' option with an empty value first
      const defaultOption = document.createElement('option');
      defaultOption.value = '';
      defaultOption.textContent = '-';
      e.appendChild(defaultOption);

      // Add new options based on the keys of this.buffers, skipping 'default'
      Object.keys(this.buffers).forEach((key) => {
        if (key === 'default') return; // Skip 'default' option

        const option = document.createElement('option');
        option.value = key;
        option.textContent = key; // or a more descriptive text

        // If the key matches the previously selected value, set it as selected
        if (selectedValue === key) {
          option.selected = true;
        }

        e.appendChild(option);
      });
    });
  }
}
