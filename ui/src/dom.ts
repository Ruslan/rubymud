function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element: ${id}`);
  }
  return element as T;
}

export function getAppElements() {
  return {
    panesContainer: requireElement<HTMLDivElement>('panes-container'),
    groupsToggle: requireElement<HTMLButtonElement>('panel-toggle-groups'),
    bottomPanels: requireElement<HTMLDivElement>('bottom-panels'),
    hotkeysBox: requireElement<HTMLDivElement>('hotkeys'),
    variablesList: requireElement<HTMLDivElement>('variables-list'),
    groupsList: requireElement<HTMLDivElement>('groups-list'),
    groupsPanel: requireElement<HTMLDivElement>('panel-groups'),
    keyboardPanel: requireElement<HTMLDivElement>('panel-keyboard'),
    variablesPanel: requireElement<HTMLDivElement>('panel-variables'),
    keyboardToggle: requireElement<HTMLButtonElement>('panel-toggle-keyboard'),
    variablesToggle: requireElement<HTMLButtonElement>('panel-toggle-variables'),
    settingsToggle: requireElement<HTMLButtonElement>('btn-open-settings'),
    connectionStatus: requireElement<HTMLSpanElement>('connection-status'),
    ticker: requireElement<HTMLSpanElement>('ticker'),
    secondaryTimers: requireElement<HTMLDivElement>('secondary-timers'),
    input: requireElement<HTMLInputElement>('input'),
  };
}

export type AppElements = ReturnType<typeof getAppElements>;

export async function fetchWithToken(url: string, options: RequestInit = {}) {
  // @ts-ignore
  const token = window.API_TOKEN || '';
  const headers = {
    ...options.headers,
    'X-Session-Token': token,
  };
  return fetch(url, { ...options, headers });
}
