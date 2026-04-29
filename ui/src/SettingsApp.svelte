<script lang="ts">
  import { onMount } from 'svelte';

  interface Variable {
    key: string;
    value: string;
  }

  interface Profile {
    id: number;
    name: string;
    description: string;
  }

  interface SessionProfile {
    id: number;
    session_id: number;
    profile_id: number;
    order_index: number;
    profile_name: string;
  }

  interface Alias {
    id?: number;
    position?: number;
    name: string;
    template: string;
    enabled: boolean;
    group_name: string;
  }

  interface Trigger {
    id?: number;
    position?: number;
    name: string;
    pattern: string;
    command: string;
    enabled: boolean;
    is_button: boolean;
    stop_after_match: boolean;
    group_name: string;
    target_buffer?: string;
    buffer_action?: string;
  }

  interface Highlight {
    id?: number;
    position?: number;
    pattern: string;
    fg: string;
    bg: string;
    bold: boolean;
    faint: boolean;
    italic: boolean;
    underline: boolean;
    strikethrough: boolean;
    blink: boolean;
    reverse: boolean;
    enabled: boolean;
    group_name: string;
  }

  interface Hotkey {
    id?: number;
    position?: number;
    shortcut: string;
    command: string;
  }

  interface ProfileVariable {
    id?: number;
    position?: number;
    name: string;
    default_value: string;
    description: string;
  }


  interface RuleGroupSummary {
    domain: string;
    group_name: string;
    total_count: number;
    enabled_count: number;
    disabled_count: number;
  }

  interface Session {
    id: number;
    name: string;
    mud_host: string;
    mud_port: number;
    status: string;
  }

  interface HistoryEntry {
    id: number;
    session_id: number;
    kind: string;
    line: string;
    created_at: string;
  }

  let currentTab = window.location.hash.slice(1) || 'variables';
  let loading = true;
  let formError = '';
  let lastFetchKey: string | null = null;
  
  let selectedSessionID: number | null = null;
  let selectedProfileID: number | null = null;
  
  let appSettings: { api_token: string } = { api_token: '' };
  const wakeLockEnabledStorageKey = 'mudhost.wakeLockEnabled';
  let wakeLockEnabled = true;

  function setWakeLockEnabled(enabled: boolean) {
    wakeLockEnabled = enabled;
    localStorage.setItem(wakeLockEnabledStorageKey, String(enabled));
  }

  window.onhashchange = () => {
    currentTab = window.location.hash.slice(1) || 'variables';
  };

  $: if (currentTab) {
    if (window.location.hash !== '#' + currentTab) {
      window.location.hash = currentTab;
    }
  }

  const tabs = [
    { id: 'sessions', label: 'Sessions' },
    { id: 'profiles', label: 'Profiles' },
    { id: 'variables', label: 'Variables' },
    { id: 'declared_variables', label: 'Declared Vars' },
    { id: 'aliases', label: 'Aliases' },
    { id: 'triggers', label: 'Triggers' },
    { id: 'highlights', label: 'Highlights' },
    { id: 'hotkeys', label: 'Hotkeys' },
    { id: 'groups', label: 'Groups' },
    { id: 'history', label: 'History' },
    { id: 'app', label: 'App' },
  ];

  // Sessions State
  let sessions: Session[] = [];
  const defaultSession = (): Partial<Session> => ({ name: '', mud_host: '', mud_port: 0 });
  let sessionEditor: Partial<Session> = defaultSession();
  $: currentSession = sessions.find(s => s.id === selectedSessionID);

  let sessionProfiles: SessionProfile[] = [];

  // History State
  let historyEntries: HistoryEntry[] = [];

  // Profiles State
  let profiles: Profile[] = [];
  const defaultProfile = (): Partial<Profile> => ({ name: '', description: '' });
  let profileEditor: Partial<Profile> = defaultProfile();
  $: currentProfile = profiles.find(p => p.id === selectedProfileID);

  // Variables State (Session-scoped)
  let variables: Variable[] = [];
  let newVarKey = '';
  let newVarValue = '';

  // Aliases State
  let aliases: Alias[] = [];
  const defaultAlias = (): Alias => ({ name: '', template: '', enabled: true, group_name: '' });
  let aliasEditor: Alias = defaultAlias();

  // Triggers State
  let triggers: Trigger[] = [];
  const defaultTrigger = (): Trigger => ({ name: '', pattern: '', command: '', enabled: true, is_button: false, stop_after_match: false, group_name: '', target_buffer: '', buffer_action: '' });
  let triggerEditor: Trigger = defaultTrigger();

  // Highlights State
  let highlights: Highlight[] = [];
  const defaultHighlight = (): Highlight => ({ pattern: '', fg: '', bg: '', bold: false, faint: false, italic: false, underline: false, strikethrough: false, blink: false, reverse: false, enabled: true, group_name: '' });
  let highlightEditor: Highlight = defaultHighlight();

  // Hotkeys State
  let hotkeys: Hotkey[] = [];
  const defaultHotkey = (): Hotkey => ({ shortcut: '', command: '' });
  let hotkeyEditor: Hotkey = defaultHotkey();

  // Profile Variables State
  let profileVariables: ProfileVariable[] = [];
  const defaultProfileVariable = (): ProfileVariable => ({ name: '', default_value: '', description: '' });
  let profileVariableEditor: ProfileVariable = defaultProfileVariable();


  // Groups State
  let groups: RuleGroupSummary[] = [];

  // Profile Files State
  let profileFiles: { filename: string }[] = [];

  type RGB = { r: number; g: number; b: number };

  const ansiBasePalette: RGB[] = [
    { r: 0, g: 0, b: 0 }, { r: 128, g: 0, b: 0 }, { r: 0, g: 128, b: 0 }, { r: 128, g: 128, b: 0 },
    { r: 0, g: 0, b: 128 }, { r: 128, g: 0, b: 128 }, { r: 0, g: 128, b: 128 }, { r: 192, g: 192, b: 192 },
    { r: 128, g: 128, b: 128 }, { r: 255, g: 0, b: 0 }, { r: 0, g: 255, b: 0 }, { r: 255, g: 255, b: 0 },
    { r: 0, g: 0, b: 255 }, { r: 255, g: 0, b: 255 }, { r: 0, g: 255, b: 255 }, { r: 255, g: 255, b: 255 },
  ];

  function ansi256ToRGB(index: number): RGB {
    if (index >= 0 && index < 16) return ansiBasePalette[index] ?? ansiBasePalette[0]!;
    if (index >= 16 && index <= 231) {
      const cube = index - 16;
      const steps = [0, 95, 135, 175, 215, 255];
      return { r: steps[Math.floor(cube / 36) % 6] ?? 0, g: steps[Math.floor(cube / 6) % 6] ?? 0, b: steps[cube % 6] ?? 0 };
    }
    const gray = 8 + (index - 232) * 10;
    return { r: gray, g: gray, b: gray };
  }

  function rgbToHex({ r, g, b }: RGB): string {
    const toHex = (value: number) => value.toString(16).padStart(2, '0');
    return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
  }

  function parseHexColor(value: string): RGB | null {
    const normalized = value.trim().toLowerCase();
    if (!normalized.startsWith('#')) return null;
    let hex = normalized.slice(1);
    if (hex.length === 3) hex = `${hex[0]}${hex[0]}${hex[1]}${hex[1]}${hex[2]}${hex[2]}`;
    if (!/^[0-9a-f]{6}$/.test(hex)) return null;
    return { r: parseInt(hex.slice(0, 2), 16), g: parseInt(hex.slice(2, 4), 16), b: parseInt(hex.slice(4, 6), 16) };
  }

  function colorValueToHex(value: string): string {
    const normalized = value.trim().toLowerCase();
    if (!normalized) return '#cccccc';
    const parsedHex = parseHexColor(normalized);
    if (parsedHex) return rgbToHex(parsedHex);
    if (normalized.startsWith('256:')) {
      const index = Number.parseInt(normalized.slice(4), 10);
      if (!Number.isNaN(index) && index >= 0 && index <= 255) return rgbToHex(ansi256ToRGB(index));
    }
    return '#cccccc';
  }

  function colorValueToCss(value: string, fallback: string): string {
    return colorValueToHex(value) === '#cccccc' && !value ? fallback : colorValueToHex(value);
  }

  function nearestAnsi256Index(rgb: RGB): number {
    let bestIndex = 0;
    let bestDistance = Number.POSITIVE_INFINITY;
    for (let i = 0; i <= 255; i += 1) {
      const candidate = ansi256ToRGB(i);
      const dr = rgb.r - candidate.r;
      const dg = rgb.g - candidate.g;
      const db = rgb.b - candidate.b;
      const distance = dr * dr + dg * dg + db * db;
      if (distance < bestDistance) { bestDistance = distance; bestIndex = i; }
    }
    return bestIndex;
  }

  function setHighlightColor(field: 'fg' | 'bg', hex: string) {
    const rgb = parseHexColor(hex);
    if (!rgb) return;
    highlightEditor = { ...highlightEditor, [field]: `256:${nearestAnsi256Index(rgb)}` };
  }

  async function fetchData() {
    loading = true;
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const headers = { 'X-Session-Token': token };
    try {
      const [sessRes, profRes] = await Promise.all([
        fetch('/api/sessions', { headers }),
        fetch('/api/profiles', { headers })
      ]);
      sessions = await sessRes.json() || [];
      profiles = await profRes.json() || [];

      if (selectedSessionID === null && sessions.length > 0) selectedSessionID = sessions[0]!.id;
      if (selectedProfileID === null && profiles.length > 0) selectedProfileID = profiles[0]!.id;

      if (currentTab === 'app') {
        const appRes = await fetch('/api/app/settings', { headers });
        appSettings = await appRes.json();
      } else if (currentTab === 'profiles') {
        const filesRes = await fetch('/api/profiles/files', { headers });
        profileFiles = await filesRes.json() || [];
      } else if (currentTab === 'sessions' && selectedSessionID) {
        const spRes = await fetch(`/api/sessions/${selectedSessionID}/profiles`, { headers });
        sessionProfiles = await spRes.json() || [];
      } else if (currentTab === 'variables' && selectedSessionID) {
        const varRes = await fetch(`/api/sessions/${selectedSessionID}/variables`, { headers });
        variables = await varRes.json() || [];
      } else if (currentTab === 'history' && selectedSessionID) {
        const histRes = await fetch(`/api/sessions/${selectedSessionID}/history`, { headers });
        historyEntries = await histRes.json() || [];
      } else if (['aliases', 'triggers', 'highlights', 'hotkeys', 'groups', 'declared_variables'].includes(currentTab) && selectedProfileID) {
        const endpoint = currentTab === 'declared_variables' ? 'variables' : currentTab;
        const res = await fetch(`/api/profiles/${selectedProfileID}/${endpoint}`, { headers });
        const data = await res.json() || [];
        if (currentTab === 'aliases') aliases = data;
        else if (currentTab === 'triggers') triggers = data;
        else if (currentTab === 'highlights') highlights = data;
        else if (currentTab === 'hotkeys') hotkeys = data;
        else if (currentTab === 'groups') groups = data;
        else if (currentTab === 'declared_variables') profileVariables = data;
      }
    } catch (e) {
      console.error(`Failed to fetch data`, e);
    } finally {
      loading = false;
    }
  }

  async function saveItem(domain: string, item: any, resetFn: () => void) {
    const validationError = validateItem(domain, item);
    if (validationError) { formError = validationError; return; }

    formError = '';
    const isUpdate = domain !== 'variables' && !!item.id;
    let url = `/api/${domain}`;
    
    if (domain === 'variables' && selectedSessionID) {
      url = `/api/sessions/${selectedSessionID}/variables`;
    } else if (['aliases', 'triggers', 'highlights', 'hotkeys', 'declared_variables'].includes(domain) && selectedProfileID) {
      const endpoint = domain === 'declared_variables' ? 'variables' : domain;
      url = `/api/profiles/${selectedProfileID}/${endpoint}`;
      if (isUpdate) url += `/${item.id}`;
    } else if (domain === 'sessions' && isUpdate) {
      url += `/${item.id}`;
    } else if (domain === 'profiles' && isUpdate) {
      url += `/${item.id}`;
    }
    
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const method = isUpdate ? 'PUT' : 'POST';
    const body = domain === 'variables' ? { key: newVarKey, value: newVarValue } : item;

    await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(body)
    });
    resetFn();
    await fetchData();
  }

  function validateItem(domain: string, item: any): string {
    if (domain === 'variables') return !newVarKey.trim() ? 'Variable key is required.' : '';
    if (domain === 'aliases') return !item.name?.trim() ? 'Alias name is required.' : (!item.template?.trim() ? 'Alias template is required.' : '');
    if (domain === 'triggers') return !item.pattern?.trim() ? 'Trigger pattern is required.' : (!item.command?.trim() ? 'Trigger command is required.' : '');
    if (domain === 'highlights') return !item.pattern?.trim() ? 'Highlight pattern is required.' : '';
    if (domain === 'hotkeys') return !item.shortcut?.trim() ? 'Shortcut is required.' : (!item.command?.trim() ? 'Command is required.' : '');
    if (domain === 'declared_variables') return !item.name?.trim() ? 'Variable name is required.' : '';
    if (domain === 'sessions') return !item.name?.trim() ? 'Session name is required.' : (!item.mud_host?.trim() ? 'MUD host is required.' : (!item.mud_port ? 'MUD port is required.' : ''));
    if (domain === 'profiles') return !item.name?.trim() ? 'Profile name is required.' : '';
    return '';
  }

  async function deleteItem(domain: string, id: string | number) {
    if (!confirm(`Delete this ${domain.slice(0, -1)}?`)) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    let url = `/api/${domain}/${id}`;
    if (domain === 'variables' && selectedSessionID) {
        url = `/api/sessions/${selectedSessionID}/variables/${encodeURIComponent(String(id))}`;
    } else if (domain === 'history' && selectedSessionID) {
        url = `/api/sessions/${selectedSessionID}/history/${id}`;
    } else if (['aliases', 'triggers', 'highlights', 'hotkeys', 'declared_variables'].includes(domain) && selectedProfileID) {
        const endpoint = domain === 'declared_variables' ? 'variables' : domain;
        url = `/api/profiles/${selectedProfileID}/${endpoint}/${id}`;
    }
    await fetch(url, { method: 'DELETE', headers: { 'X-Session-Token': token } });
    await fetchData();
  }

  async function toggleItem(domain: 'aliases' | 'triggers' | 'highlights', item: any, enabled: boolean) {
    if (!selectedProfileID) return;
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/profiles/${selectedProfileID}/${domain}/${item.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({ ...item, enabled }),
    });
    await fetchData();
  }

  async function toggleGroup(domain: string, groupName: string, enabled: boolean) {
    if (!selectedProfileID) return;
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/profiles/${selectedProfileID}/groups/toggle`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({ domain, group_name: groupName, enabled }),
    });
    await fetchData();
  }

  async function sessionAction(action: 'connect' | 'disconnect', id: number) {
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/sessions/${id}/${action}`, { method: 'POST', headers: { 'X-Session-Token': token } });
    await fetchData();
  }
  
  async function addProfileToSession(profileID: number) {
    if (!selectedSessionID) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const maxOrder = sessionProfiles.length > 0 ? Math.max(...sessionProfiles.map(sp => sp.order_index)) : -1;
    await fetch(`/api/sessions/${selectedSessionID}/profiles`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({ profile_id: profileID, order_index: maxOrder + 1 })
    });
    await fetchData();
  }
  
  async function removeProfileFromSession(profileID: number) {
    if (!selectedSessionID) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/sessions/${selectedSessionID}/profiles/${profileID}`, { method: 'DELETE', headers: { 'X-Session-Token': token } });
    await fetchData();
  }
  
  async function moveSessionProfile(index: number, direction: -1 | 1) {
    if (!selectedSessionID) return;
    if (index + direction < 0 || index + direction >= sessionProfiles.length) return;
    
    const newProfiles = [...sessionProfiles];
    const temp = newProfiles[index];
    newProfiles[index] = newProfiles[index + direction]!;
    newProfiles[index + direction] = temp!;
    
    const payload = newProfiles.map((sp, i) => ({ profile_id: sp.profile_id, order_index: i }));
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/sessions/${selectedSessionID}/profiles/reorder`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(payload)
    });
    await fetchData();
  }
  
  async function exportProfile(profileID: number) {
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch(`/api/profiles/${profileID}/export`, { method: 'POST', headers: { 'X-Session-Token': token } });
    if (!res.ok) { alert('Export failed: ' + await res.text()); return; }
    const data = await res.json();
    alert(`Exported to config/${data.filename}`);
    await fetchData();
  }

  async function exportAllProfiles() {
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch('/api/profiles/export/all', { method: 'POST', headers: { 'X-Session-Token': token } });
    if (!res.ok) { alert('Export failed: ' + await res.text()); return; }
    const filenames: string[] = await res.json();
    alert(`Exported ${filenames.length} profile(s) to config/`);
    await fetchData();
  }

  async function importAllProfiles() {
    if (!confirm('Import all .tt files from config/? This will create new profiles for each file.')) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch('/api/profiles/import/all', {
      method: 'POST',
      headers: { 'X-Session-Token': token }
    });
    if (!res.ok) { alert('Import failed: ' + await res.text()); return; }
    await fetchData();
  }

  async function importProfileFromFile(filename: string) {
    if (!confirm(`Import profile from ${filename}?`)) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch('/api/profiles/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({ filename, session_id: selectedSessionID })
    });
    if (!res.ok) { alert('Import failed: ' + await res.text()); return; }
    await fetchData();
  }
  
  async function moveRule(domain: string, item: any, direction: -1 | 1) {
    if (!selectedProfileID) return;
    
    let list: any[] = [];
    if (domain === 'aliases') list = aliases;
    else if (domain === 'triggers') list = triggers;
    else if (domain === 'highlights') list = highlights;
    else if (domain === 'hotkeys') list = hotkeys;
    else if (domain === 'declared_variables') list = profileVariables;
    
    const index = list.findIndex(x => x.id === item.id);
    if (index === -1 || index + direction < 0 || index + direction >= list.length) return;
    
    const swapWith = list[index + direction];
    const currentPos = item.position;
    const swapPos = swapWith.position;
    
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const headers = { 'Content-Type': 'application/json', 'X-Session-Token': token };
    
    // Optimistic update
    item.position = swapPos;
    swapWith.position = currentPos;
    
    const endpoint = domain === 'declared_variables' ? 'variables' : domain;
    await Promise.all([
      fetch(`/api/profiles/${selectedProfileID}/${endpoint}/${item.id}`, { method: 'PUT', headers, body: JSON.stringify(item) }),
      fetch(`/api/profiles/${selectedProfileID}/${endpoint}/${swapWith.id}`, { method: 'PUT', headers, body: JSON.stringify(swapWith) })
    ]);
    
    await fetchData();
  }

  async function rotateAPIToken() {
    if (!confirm('Are you sure you want to rotate the API token? This will invalidate all external clients.')) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch('/api/app/settings/rotate-api-token', { method: 'POST', headers: { 'X-Session-Token': token } });
    appSettings = await res.json();
    // @ts-ignore
    window.API_TOKEN = appSettings.api_token;
  }

  function titleCase(value: string): string {
    return value.slice(0, 1).toUpperCase() + value.slice(1);
  }

  function ruleCountForGroup(domain: string, groupName: string): number {
    const normalized = groupName || 'default';
    if (domain === 'aliases') return aliases.filter((item) => (item.group_name || 'default') === normalized).length;
    if (domain === 'triggers') return triggers.filter((item) => (item.group_name || 'default') === normalized).length;
    if (domain === 'highlights') return highlights.filter((item) => (item.group_name || 'default') === normalized).length;
    return 0;
  }

  $: {
    const fetchKey = `${currentTab}|${selectedSessionID ?? ''}|${selectedProfileID ?? ''}`;
    if (lastFetchKey !== null && fetchKey !== lastFetchKey) {
      lastFetchKey = fetchKey;
      void fetchData();
    }
  }

  onMount(() => {
    wakeLockEnabled = localStorage.getItem(wakeLockEnabledStorageKey) !== 'false';
    lastFetchKey = `${currentTab}|${selectedSessionID ?? ''}|${selectedProfileID ?? ''}`;
    void fetchData();
  });
</script>

<main class="settings-layout">
  <nav class="sidebar">
    <div class="sidebar-header"><h1>Settings</h1></div>
    <ul class="nav-links">
      {#each tabs as tab}
        <li class:active={currentTab === tab.id}>
          <button on:click={() => currentTab = tab.id}>{tab.label}</button>
        </li>
      {/each}
    </ul>
  </nav>

  <section class="content">
    {#if ['variables', 'sessions', 'history'].includes(currentTab) && currentTab !== 'sessions'}
        <div class="selector-box">
            <label for="session-selector">Configuring Session:</label>
            <select id="session-selector" bind:value={selectedSessionID}>
                {#each sessions as s}
                    <option value={s.id}>{s.name} ({s.mud_host}:{s.mud_port})</option>
                {/each}
                {#if sessions.length === 0}
                    <option value={null}>No sessions available</option>
                {/if}
            </select>
        </div>
    {/if}
    
    {#if ['aliases', 'triggers', 'highlights', 'groups', 'hotkeys', 'declared_variables'].includes(currentTab)}
        <div class="selector-box">
            <label for="profile-selector">Configuring Profile:</label>
            <select id="profile-selector" bind:value={selectedProfileID}>
                {#each profiles as p}
                    <option value={p.id}>{p.name}</option>
                {/each}
                {#if profiles.length === 0}
                    <option value={null}>No profiles available</option>
                {/if}
            </select>
        </div>
    {/if}

    {#if currentTab === 'variables'}
      <header class="content-header"><h2>Variables</h2><p class="description">State variables for {currentSession?.name || 'selected session'}.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-row">
          <input type="text" bind:value={newVarKey} placeholder="Key" required />
          <input type="text" bind:value={newVarValue} placeholder="Value" />
          <button class="btn-primary" on:click={() => saveItem('variables', {}, () => { newVarKey = ''; newVarValue = ''; })}>Save</button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th>Key</th><th>Value</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each variables as v}
            <tr>
              <td class="key-cell">{v.key}</td><td class="value-cell">{v.value}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => { newVarKey = v.key; newVarValue = v.value; }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('variables', v.key)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>

    {:else if currentTab === 'declared_variables'}
      <header class="content-header"><h2>Declared Variables</h2><p class="description">Variables declared by profile {currentProfile?.name}.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-row">
          <input type="text" bind:value={profileVariableEditor.name} placeholder="Name" required />
          <input type="text" bind:value={profileVariableEditor.default_value} placeholder="Default Value" />
          <input type="text" bind:value={profileVariableEditor.description} placeholder="Description" />
          <button class="btn-primary" on:click={() => saveItem('declared_variables', profileVariableEditor, () => profileVariableEditor = defaultProfileVariable())}>
            {profileVariableEditor.id ? 'Update' : 'Add'}
          </button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th style="width: 60px">Order</th><th>Name</th><th>Default Value</th><th>Description</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each profileVariables as v, i}
            <tr>
              <td class="order-cell">
                 <button class="btn-icon" disabled={i === 0} on:click={() => moveRule('declared_variables', v, -1)}>▲</button>
                 <button class="btn-icon" disabled={i === profileVariables.length - 1} on:click={() => moveRule('declared_variables', v, 1)}>▼</button>
              </td>
              <td class="key-cell">{v.name}</td><td class="value-cell">{v.default_value || '-'}</td>
              <td class="dim-cell">{v.description || '-'}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => profileVariableEditor = { ...v }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('declared_variables', v.id!)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>


    {:else if currentTab === 'aliases'}
      <header class="content-header"><h2>Aliases</h2><p class="description">Command shortcuts for profile {currentProfile?.name}.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-row">
          <input type="text" bind:value={aliasEditor.name} placeholder="Name" required />
          <input type="text" bind:value={aliasEditor.template} placeholder="Template" required />
          <input type="text" bind:value={aliasEditor.group_name} placeholder="Group Name" />
          <label class="checkbox-label"><input type="checkbox" bind:checked={aliasEditor.enabled} /> Enabled</label>
          <button class="btn-primary" on:click={() => saveItem('aliases', aliasEditor, () => aliasEditor = defaultAlias())}>
            {aliasEditor.id ? 'Update' : 'Add'}
          </button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th style="width: 60px">Order</th><th style="width: 72px">Status</th><th>Name</th><th>Template</th><th>Group</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each aliases as a, i}
            <tr>
              <td class="order-cell">
                 <button class="btn-icon" disabled={i === 0} on:click={() => moveRule('aliases', a, -1)}>▲</button>
                 <button class="btn-icon" disabled={i === aliases.length - 1} on:click={() => moveRule('aliases', a, 1)}>▼</button>
              </td>
              <td>
                <label class="toggle-label">
                  <input type="checkbox" checked={a.enabled} on:change={(event) => toggleItem('aliases', a, (event.currentTarget as HTMLInputElement).checked)} />
                  <span class="status-dot {a.enabled ? 'on' : 'off'}" title={a.enabled ? 'Enabled' : 'Disabled'}></span>
                </label>
              </td>
              <td class="key-cell">{a.name}</td><td class="value-cell">{a.template}</td>
              <td class="dim-cell">{a.group_name || '-'}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => aliasEditor = { ...a }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('aliases', a.id!)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>

    {:else if currentTab === 'triggers'}
      <header class="content-header"><h2>Triggers</h2><p class="description">Automatic reactions for profile {currentProfile?.name}.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-grid">
          <div class="form-row"><input type="text" bind:value={triggerEditor.name} placeholder="Trigger Name" /><input type="text" bind:value={triggerEditor.pattern} placeholder="Pattern (Regex)" required /></div>
          <div class="form-row"><input type="text" bind:value={triggerEditor.command} placeholder="Command" required /><input type="text" bind:value={triggerEditor.group_name} placeholder="Group Name" /></div>
          <div class="form-row" style="grid-template-columns: 120px 1fr;">
            <select bind:value={triggerEditor.buffer_action}>
              <option value="">No Routing</option>
              <option value="move">Move</option>
              <option value="copy">Copy</option>
              <option value="echo">Echo</option>
            </select>
            <input type="text" bind:value={triggerEditor.target_buffer} placeholder="Target Buffer (e.g. chat)" disabled={!triggerEditor.buffer_action} />
          </div>
          <div class="form-row options-row">
            <label class="checkbox-label"><input type="checkbox" bind:checked={triggerEditor.enabled} /> Enabled</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={triggerEditor.is_button} /> Is Button</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={triggerEditor.stop_after_match} /> Stop After Match</label>
            <button class="btn-primary" on:click={() => saveItem('triggers', triggerEditor, () => triggerEditor = defaultTrigger())}>
              {triggerEditor.id ? 'Update' : 'Add'}
            </button>
          </div>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th style="width: 60px">Order</th><th style="width: 96px">Flags</th><th>Pattern</th><th>Command</th><th>Group</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each triggers as t, i}
            <tr>
              <td class="order-cell">
                 <button class="btn-icon" disabled={i === 0} on:click={() => moveRule('triggers', t, -1)}>▲</button>
                 <button class="btn-icon" disabled={i === triggers.length - 1} on:click={() => moveRule('triggers', t, 1)}>▼</button>
              </td>
              <td class="flags-cell">
                <div class="flags-wrapper">
                  <label class="toggle-label" title="Enabled">
                    <input type="checkbox" checked={t.enabled} on:change={(event) => toggleItem('triggers', t, (event.currentTarget as HTMLInputElement).checked)} />
                    <span class={t.enabled ? 'flag-on' : 'flag-off'}>●</span>
                  </label>
                  {#if t.is_button}<span class="flag-icon" title="Is Button">⚡</span>{/if}
                  {#if t.stop_after_match}<span class="flag-icon" title="Stop After Match">🛑</span>{/if}
                </div>
              </td>
              <td>
                <div class="key-cell">{t.pattern}</div>
                <div class="comment-text">{t.name || '(no name)'}</div>
              </td>
              <td class="value-cell">
                {t.command}
                {#if t.buffer_action && t.target_buffer}
                  <div class="routing-hint" style="font-size:11px; color:#58a6ff; margin-top:2px;">↳ {t.buffer_action} to [{t.target_buffer}]</div>
                {/if}
              </td>
              <td class="dim-cell">{t.group_name || '-'}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => triggerEditor = { ...t }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('triggers', t.id!)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>

    {:else if currentTab === 'highlights'}
      <header class="content-header"><h2>Highlights</h2><p class="description">Visual formatting for profile {currentProfile?.name}.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-grid">
          <div class="form-row">
            <input type="text" bind:value={highlightEditor.pattern} placeholder="Pattern (Regex)" required />
            <input type="text" bind:value={highlightEditor.group_name} placeholder="Group Name" />
          </div>
          <div class="form-row">
            <label class="color-field">
              <span>FG</span>
              <input type="color" value={colorValueToHex(highlightEditor.fg)} on:input={(event) => setHighlightColor('fg', (event.currentTarget as HTMLInputElement).value)} />
              <code>{highlightEditor.fg || 'default'}</code>
            </label>
            <label class="color-field">
              <span>BG</span>
              <input type="color" value={colorValueToHex(highlightEditor.bg)} on:input={(event) => setHighlightColor('bg', (event.currentTarget as HTMLInputElement).value)} />
              <code>{highlightEditor.bg || 'default'}</code>
            </label>
          </div>
          <div class="form-row options-row">
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.enabled} /> Enabled</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.bold} /> Bold</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.faint} /> Faint</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.italic} /> Italic</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.underline} /> Underline</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.strikethrough} /> Strikethrough</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.blink} /> Blink</label>
            <label class="checkbox-label"><input type="checkbox" bind:checked={highlightEditor.reverse} /> Reverse</label>
            <button class="btn-primary" on:click={() => saveItem('highlights', highlightEditor, () => highlightEditor = defaultHighlight())}>
              {highlightEditor.id ? 'Update' : 'Add'}
            </button>
          </div>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th style="width: 60px">Order</th><th style="width: 72px">Status</th><th>Pattern</th><th>Colors</th><th>Group</th><th>Preview</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each highlights as h, i}
            <tr>
              <td class="order-cell">
                 <button class="btn-icon" disabled={i === 0} on:click={() => moveRule('highlights', h, -1)}>▲</button>
                 <button class="btn-icon" disabled={i === highlights.length - 1} on:click={() => moveRule('highlights', h, 1)}>▼</button>
              </td>
              <td>
                <label class="toggle-label">
                  <input type="checkbox" checked={h.enabled} on:change={(event) => toggleItem('highlights', h, (event.currentTarget as HTMLInputElement).checked)} />
                  <span class="status-dot {h.enabled ? 'on' : 'off'}" title={h.enabled ? 'Enabled' : 'Disabled'}></span>
                </label>
              </td>
              <td class="key-cell">{h.pattern}</td>
              <td class="dim-cell">
                <div class="color-preview">
                  <span class="color-chip" style="background: {colorValueToCss(h.fg, '#cccccc')}"></span> {h.fg || 'def'}
                  <span class="color-chip" style="background: {colorValueToCss(h.bg, '#333333')}"></span> {h.bg || 'def'}
                </div>
              </td>
              <td class="dim-cell">{h.group_name || '-'}</td>
              <td class="value-cell">
                <span style="
                  color: {h.reverse ? colorValueToCss(h.bg, '#111111') : colorValueToCss(h.fg, '#eeeeee')};
                  background: {h.reverse ? colorValueToCss(h.fg, '#eeeeee') : colorValueToCss(h.bg, 'transparent')};
                  font-weight: {h.bold ? 'bold' : 'normal'};
                  opacity: {h.faint ? '0.6' : '1'};
                  font-style: {h.italic ? 'italic' : 'normal'};
                  text-decoration: {h.underline ? (h.strikethrough ? 'underline line-through' : 'underline') : (h.strikethrough ? 'line-through' : 'none')};
                  animation: {h.blink ? 'blink 1s infinite' : 'none'}
                ">
                  Sample Text
                </span>
              </td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => highlightEditor = { ...h }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('highlights', h.id!)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      
    {:else if currentTab === 'hotkeys'}
      <header class="content-header"><h2>Hotkeys</h2><p class="description">Keyboard shortcuts for profile {currentProfile?.name}.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-row">
          <input type="text" bind:value={hotkeyEditor.shortcut} placeholder="Shortcut (e.g. F1, Ctrl+A)" required />
          <input type="text" bind:value={hotkeyEditor.command} placeholder="Command" required />
          <button class="btn-primary" on:click={() => saveItem('hotkeys', hotkeyEditor, () => hotkeyEditor = defaultHotkey())}>
            {hotkeyEditor.id ? 'Update' : 'Add'}
          </button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th style="width: 60px">Order</th><th>Shortcut</th><th>Command</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each hotkeys as h, i}
            <tr>
              <td class="order-cell">
                 <button class="btn-icon" disabled={i === 0} on:click={() => moveRule('hotkeys', h, -1)}>▲</button>
                 <button class="btn-icon" disabled={i === hotkeys.length - 1} on:click={() => moveRule('hotkeys', h, 1)}>▼</button>
              </td>
              <td class="key-cell">{h.shortcut}</td><td class="value-cell">{h.command}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => hotkeyEditor = { ...h }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('hotkeys', h.id!)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>

    {:else if currentTab === 'groups'}
      <header class="content-header"><h2>Groups</h2><p class="description">Bulk enable/disable rule groups for profile {currentProfile?.name}.</p></header>
      {#if formError}<div class="form-error">{formError}</div>{/if}
      <table class="data-table">
        <thead><tr><th>Domain</th><th>Group</th><th>Rules</th><th>Status</th><th style="width: 180px">Actions</th></tr></thead>
        <tbody>
          {#each groups as group}
            <tr>
              <td class="key-cell">{titleCase(group.domain)}</td>
              <td class="value-cell">{group.group_name || 'default'}</td>
              <td class="dim-cell">{ruleCountForGroup(group.domain, group.group_name)} total</td>
              <td class="dim-cell">{group.enabled_count} on / {group.disabled_count} off</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => toggleGroup(group.domain, group.group_name, true)}>Enable</button>
                <button class="btn-link btn-danger" on:click={() => toggleGroup(group.domain, group.group_name, false)}>Disable</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      
    {:else if currentTab === 'profiles'}
      <header class="content-header"><h2>Profiles</h2><p class="description">Manage settings profiles.</p></header>
      
      <div class="editor-box" style="display: flex; gap: 12px; align-items: center; margin-bottom: 24px;">
        <button class="btn-primary" on:click={exportAllProfiles}>Export All to config/</button>
        <button class="btn-primary" style="background: #2ecc71" on:click={importAllProfiles}>Import All from config/</button>
      </div>

      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-row">
            <input type="text" bind:value={profileEditor.name} placeholder="Profile Name" required />
            <input type="text" bind:value={profileEditor.description} placeholder="Description" />
            <button class="btn-primary" on:click={() => saveItem('profiles', profileEditor, () => profileEditor = defaultProfile())}>
                {profileEditor.id ? 'Update' : 'Add'}
            </button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th>Name</th><th>Description</th><th>Actions</th></tr></thead>
        <tbody>
          {#each profiles as p}
            <tr>
              <td class="key-cell">{p.name}</td>
              <td class="value-cell">{p.description || '-'}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => exportProfile(p.id)}>Export</button>
                <button class="btn-link" style="margin-left: 12px" on:click={() => profileEditor = { ...p }}>Edit</button>
                <button class="btn-link btn-danger" style="margin-left: 12px" on:click={() => deleteItem('profiles', p.id)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>

      <h3 style="margin-top: 40px; border-bottom: 1px solid #2d333b; padding-bottom: 8px;">Files in config/</h3>
      <table class="data-table" style="margin-bottom: 20px;">
        <thead><tr><th>Filename</th><th style="width: 120px">Actions</th></tr></thead>
        <tbody>
            {#each profileFiles as file}
                <tr>
                    <td class="value-cell">{file.filename}</td>
                    <td class="actions-cell">
                        <button class="btn-link" on:click={() => importProfileFromFile(file.filename)}>Import</button>
                    </td>
                </tr>
            {/each}
            {#if profileFiles.length === 0}
                <tr><td colspan="2" class="dim-cell">No .tt files found in config/ directory.</td></tr>
            {/if}
        </tbody>
      </table>

    {:else if currentTab === 'sessions'}
      <header class="content-header"><h2>Sessions</h2><p class="description">Manage your MUD connections and active profiles.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-row">
            <input type="text" bind:value={sessionEditor.name} placeholder="Session Name" required />
            <input type="text" bind:value={sessionEditor.mud_host} placeholder="MUD Host" required />
            <input type="number" bind:value={sessionEditor.mud_port} placeholder="Port" required />
            <button class="btn-primary" on:click={() => saveItem('sessions', sessionEditor, () => sessionEditor = defaultSession())}>
                {sessionEditor.id ? 'Update' : 'Add'}
            </button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th>Status</th><th>Name</th><th>Connection</th><th>Actions</th></tr></thead>
        <tbody>
          {#each sessions as s}
            <tr>
              <td>
                <span class="status-tag {s.status === 'connected' ? 'connected' : 'disconnected'}">
                  {s.status}
                </span>
              </td>
              <td class="key-cell">{s.name}</td>
              <td class="value-cell">{s.mud_host}:{s.mud_port}</td>
              <td class="actions-cell">
                {#if s.status === 'connected'}
                    <button class="btn-link btn-danger" on:click={() => sessionAction('disconnect', s.id)}>Disconnect</button>
                {:else}
                    <button class="btn-link" on:click={() => sessionAction('connect', s.id)}>Connect</button>
                {/if}
                <button class="btn-link" style="margin-left: 12px" on:click={() => { selectedSessionID = s.id; currentTab = 'variables'; }}>View</button>
                <button class="btn-link" style="margin-left: 12px" on:click={() => sessionEditor = { ...s }}>Edit</button>
                <button class="btn-link btn-danger" style="margin-left: 12px" on:click={() => deleteItem('sessions', s.id)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      
      {#if currentSession}
        <h3 style="margin-top: 40px; border-bottom: 1px solid #2d333b; padding-bottom: 8px;">Active Profiles for {currentSession.name}</h3>
        <p class="description">Profiles at the bottom of the list have higher priority and will override rules from profiles above them.</p>
        <table class="data-table" style="margin-bottom: 20px;">
            <thead><tr><th style="width: 60px">Order</th><th>Profile Name</th><th style="width: 120px">Actions</th></tr></thead>
            <tbody>
                {#each sessionProfiles as sp, i}
                    <tr>
                        <td class="order-cell">
                            <button class="btn-icon" disabled={i === 0} on:click={() => moveSessionProfile(i, -1)}>▲</button>
                            <button class="btn-icon" disabled={i === sessionProfiles.length - 1} on:click={() => moveSessionProfile(i, 1)}>▼</button>
                        </td>
                        <td class="value-cell">{sp.profile_name}</td>
                        <td class="actions-cell">
                            <button class="btn-link btn-danger" on:click={() => removeProfileFromSession(sp.profile_id)}>Remove</button>
                        </td>
                    </tr>
                {/each}
                {#if sessionProfiles.length === 0}
                    <tr><td colspan="3" class="dim-cell">No active profiles for this session.</td></tr>
                {/if}
            </tbody>
        </table>
        
        <div class="form-row" style="max-width: 400px">
            <select bind:value={profileEditor.id} style="flex: 1; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px; border-radius: 6px;">
                <option value={null} disabled selected>Select a profile to add</option>
                {#each profiles as p}
                    <option value={p.id}>{p.name}</option>
                {/each}
            </select>
            <button class="btn-primary" disabled={!profileEditor.id} on:click={() => { if (profileEditor.id) addProfileToSession(profileEditor.id); profileEditor.id = undefined; }}>Add</button>
        </div>
      {/if}

    {:else if currentTab === 'history'}
      <header class="content-header"><h2>History</h2><p class="description">Command history for {currentSession?.name || 'selected session'}.</p></header>
      <table class="data-table">
        <thead><tr><th>Time</th><th>Kind</th><th>Command</th><th style="width: 100px">Actions</th></tr></thead>
        <tbody>
          {#each historyEntries as entry}
            <tr>
              <td class="dim-cell" title={entry.created_at}>{new Date(entry.created_at).toLocaleString()}</td>
              <td class="dim-cell">{entry.kind}</td>
              <td class="key-cell">{entry.line}</td>
              <td class="actions-cell">
                <button class="btn-link btn-danger" on:click={() => deleteItem('history', entry.id)}>Delete</button>
              </td>
            </tr>
          {/each}
          {#if historyEntries.length === 0}
            <tr><td colspan="4" class="dim-cell">No history entries found for this session.</td></tr>
          {/if}
        </tbody>
      </table>

    {:else if currentTab === 'app'}
        <header class="content-header"><h2>App Settings</h2><p class="description">Global application configuration.</p></header>
        <div class="editor-box">
            <h3>Wake Lock</h3>
            <p class="description">Keep the screen awake while the main client tab or installed app is visible. Local to this browser/PWA.</p>
            <label class="checkbox-label">
                <input type="checkbox" checked={wakeLockEnabled} on:change={(event) => setWakeLockEnabled((event.currentTarget as HTMLInputElement).checked)} />
                Keep screen awake while active
            </label>
        </div>
        <div class="editor-box">
            <h3>API Token</h3>
            <p class="description">Use this token for external REST or WebSocket access. Header: <code>X-Session-Token</code></p>
            <div class="form-row">
                <input type="text" readonly value={appSettings.api_token} />
                <button class="btn-primary" on:click={() => navigator.clipboard.writeText(appSettings.api_token)}>Copy</button>
                <button class="btn-link btn-danger" on:click={rotateAPIToken}>Rotate Token</button>
            </div>
        </div>
    {/if}
  </section>
</main>

<style>
  :global(body) { margin: 0; font-family: sans-serif; background: #111315; color: #e8edf2; }
  @keyframes blink { 0% { opacity: 1; } 50% { opacity: 0.2; } 100% { opacity: 1; } }
  .settings-layout { display: grid; grid-template-columns: 240px 1fr; height: 100vh; }
  .sidebar { background: #1a1d21; border-right: 1px solid #2d333b; }
  .sidebar-header { padding: 24px 20px; border-bottom: 1px solid #2d333b; color: #3498db; }
  .nav-links { list-style: none; padding: 10px 0; margin: 0; }
  .nav-links li button { width: 100%; padding: 12px 20px; text-align: left; background: none; border: none; color: #9ba3af; cursor: pointer; transition: 0.2s; }
  .nav-links li.active button { background: #24292f; color: #3498db; border-left: 3px solid #3498db; }
  .content { padding: 40px; overflow-y: auto; }
  .description { color: #9ba3af; margin-bottom: 32px; }
  .form-error { color: #ff7b72; margin-bottom: 12px; font-size: 0.9rem; }
  .editor-box { background: #1a1d21; padding: 20px; border-radius: 8px; border: 1px solid #2d333b; margin-bottom: 32px; }
  .selector-box { background: #1a1d21; padding: 12px 20px; border-radius: 8px; border: 1px solid #2d333b; margin-bottom: 24px; display: flex; align-items: center; gap: 12px; }
  .selector-box select { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 6px 12px; border-radius: 6px; }
  .form-grid { display: flex; flex-direction: column; gap: 12px; }
  .form-row { display: flex; gap: 12px; align-items: center; }
  .options-row { margin-top: 8px; border-top: 1px solid #2d333b; padding-top: 12px; flex-wrap: wrap; }
  input[type="text"], input[type="number"] { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; flex: 1; }
  .checkbox-label { display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: #9ba3af; cursor: pointer; }
  .color-field { display: flex; align-items: center; gap: 10px; color: #9ba3af; min-width: 0; }
  .color-field span { font-size: 0.85rem; min-width: 20px; }
  .color-field code { color: #e8edf2; background: #0d1117; border: 1px solid #30363d; border-radius: 6px; padding: 6px 8px; min-width: 72px; }
  input[type="color"] { width: 44px; height: 32px; background: none; border: none; padding: 0; }
  .btn-primary { background: #3498db; color: white; border: none; padding: 8px 20px; border-radius: 6px; cursor: pointer; }
  .btn-primary:disabled { background: #2980b9; opacity: 0.5; cursor: not-allowed; }
  .data-table { width: 100%; border-collapse: collapse; }
  .data-table th { text-align: left; padding: 12px; border-bottom: 2px solid #30363d; color: #9ba3af; font-size: 0.8rem; text-transform: uppercase; }
  .data-table td { padding: 12px; border-bottom: 1px solid #21262d; font-size: 0.9rem; vertical-align: middle; }
  .order-cell { text-align: center; }
  .btn-icon { background: none; border: none; color: #9ba3af; cursor: pointer; padding: 2px 6px; border-radius: 4px; }
  .btn-icon:hover:not(:disabled) { background: #2d333b; color: #e8edf2; }
  .btn-icon:disabled { opacity: 0.3; cursor: not-allowed; }
  .key-cell { font-family: monospace; color: #f39c12; font-weight: 600; }
  .value-cell { color: #e8edf2; }
  .dim-cell { color: #666; font-size: 0.8rem; }
  .comment-text { font-size: 0.75rem; color: #8e96a3; margin-top: 2px; font-style: italic; opacity: 0.8; }
  .status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; vertical-align: middle; }
  .status-dot.on { background: #2ecc71; box-shadow: 0 0 5px #2ecc71; }
  .status-dot.off { background: #95a5a6; }
  .status-tag { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; font-weight: bold; text-transform: uppercase; }
  .status-tag.connected { background: rgba(46, 204, 113, 0.2); color: #2ecc71; border: 1px solid rgba(46, 204, 113, 0.3); }
  .status-tag.disconnected { background: rgba(231, 76, 60, 0.2); color: #e74c3c; border: 1px solid rgba(231, 76, 60, 0.3); }
  .flags-cell { width: 80px; }
  .flags-wrapper { display: flex; gap: 8px; align-items: center; font-size: 1.1rem; line-height: 1; }
  .flag-on { color: #2ecc71; }
  .flag-off { color: #444; }
  .flag-icon { filter: grayscale(0.5); font-size: 0.9rem; }
  .color-preview { display: flex; align-items: center; gap: 4px; }
  .color-chip { width: 12px; height: 12px; border-radius: 2px; border: 1px solid #444; }
  .actions-cell { white-space: nowrap; }
  .toggle-label { display: inline-flex; align-items: center; gap: 8px; }
  .btn-link { background: none; border: none; color: #3498db; cursor: pointer; padding: 0; font-size: 0.9rem; }
  .btn-link:hover { text-decoration: underline; }
  .btn-danger { color: #e74c3c; margin-left: 12px; }
</style>
