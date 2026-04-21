function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element: ${id}`);
  }
  return element as T;
}

export function getAppElements() {
  return {
    output: requireElement<HTMLDivElement>('output'),
    bottomPanels: requireElement<HTMLDivElement>('bottom-panels'),
    bottomToolbarHint: requireElement<HTMLSpanElement>('bottom-toolbar-hint'),
    hotkeysBox: requireElement<HTMLDivElement>('hotkeys'),
    variablesList: requireElement<HTMLDivElement>('variables-list'),
    keyboardPanel: requireElement<HTMLDivElement>('panel-keyboard'),
    variablesPanel: requireElement<HTMLDivElement>('panel-variables'),
    keyboardToggle: requireElement<HTMLButtonElement>('panel-toggle-keyboard'),
    variablesToggle: requireElement<HTMLButtonElement>('panel-toggle-variables'),
    settingsToggle: requireElement<HTMLButtonElement>('btn-open-settings'),
    connectionStatus: requireElement<HTMLSpanElement>('connection-status'),
    input: requireElement<HTMLInputElement>('input'),
  };
}

export type AppElements = ReturnType<typeof getAppElements>;
