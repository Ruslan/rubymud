class Keyboard {
  constructor() {
    const keyboardMatrix = [
      ["Esc", "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12", "F13", "F14", "F15", "F16", "F17", "F18", "F19"],
      ["`", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-", "=", "Delete", "", "home", "pageup", "/", "*", "-"],
      ["Tab", "Q", "W", "E", "R", "T", "Y", "U", "I", "O", "P", "[", "]", "\\", "", "end", "pagedown", "num_7", "num_8", "num_9"],
      ["Caps Lock", "A", "S", "D", "F", "G", "H", "J", "K", "L", ";", "'", "Return", "", "", "", "num_4", "num_5", "num_6"],
      ["Shift", "Z", "X", "C", "V", "B", "N", "M", ",", ".", "/", "Shift", "", "↑", "", "num_1", "num_2", "num_3", "Enter"],
      ["Control", "Option", "Command", " ", "Command", "Option", "", "←", "↓", "→", "", "", "", "0", "", "."]
    ]
    this.keyboardMatrix = keyboardMatrix.map(row => row.map(key => key.toLowerCase()));
  }

  render(hotkeysList) {
    const main = document.querySelector(".virtual-keyboard")
    main.innerHTML = ''
    let preparedHotkeys = []
    hotkeysList.map(hk => {
      let btns = hk[0].split('+')
      let btn = btns[btns.length - 1].toLowerCase()
      preparedHotkeys[btn] = [hk[1], hk[0]]
    })
    this.keyboardMatrix.forEach(row => {
      let rowDiv = document.createElement('div')
      row.forEach(button => {
        if (preparedHotkeys[button]) {
          let btnSpan = document.createElement('span')
          let buttonData = preparedHotkeys[button];
          let label = buttonData[0]
          if (!label) return;
          if (label.length > 6) label = label.substring(0, 5) + '…'
          btnSpan.innerText = label
          btnSpan.className = 'server-line__button'
          btnSpan.addEventListener('click', () => {
            hotkeys.trigger(buttonData[1])
          })
          rowDiv.append(btnSpan)
        } else {
          let btnSpan = document.createElement('span')
          btnSpan.innerHTML = '&nbsp;'
          btnSpan.className = 'server-line__button'
          // rowDiv.append(btnSpan)
        }
      })
      main.append(rowDiv)
    });
  }
}
