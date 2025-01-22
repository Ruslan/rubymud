class Buffers {
  constructor() {
    this.mainBuffer = document.getElementById('main_output')
    this.buffers = {};
    this.buffers['default'] = this.mainBuffer
  }

  getBuffer(name) {
    if (this.buffers[name]) return this.buffers[name]

    let currentBuffer = document.createElement('div')
    currentBuffer.className = 'output'
    currentBuffer.setAttribute("data-buffer", name)
    this.buffers[name] = currentBuffer
    return currentBuffer
  }

  appendTo(mainName, div) {
    if (!mainName) mainName = 'default'
    let currentOut = this.getBuffer(mainName)
    const shouldScroll = currentOut.parentNode && Math.abs(currentOut.parentNode.scrollTop - (currentOut.scrollHeight - currentOut.parentNode.offsetHeight)) < 20;
    currentOut.append(div)
    if (shouldScroll) {
      currentOut.parentNode.scrollTop = currentOut.scrollHeight;
    }
  }
}
