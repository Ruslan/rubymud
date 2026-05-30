<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import AliasesSection from './settings/AliasesSection.svelte';
  import SubstitutionsSection from './settings/SubstitutionsSection.svelte';
  import TriggersSection from './settings/TriggersSection.svelte';
  import HighlightsSection from './settings/HighlightsSection.svelte';
  import HotkeysSection from './settings/HotkeysSection.svelte';
  import TimersSection from './settings/TimersSection.svelte';
  import GroupsSection from './settings/GroupsSection.svelte';
  import DeclaredVariablesSection from './settings/DeclaredVariablesSection.svelte';
  import VariablesSection from './settings/VariablesSection.svelte';
  import ProfilesSection from './settings/ProfilesSection.svelte';
  import SessionsSection from './settings/SessionsSection.svelte';
  import type {
    Alias,
    Highlight,
    HistoryEntry,
    Hotkey,
    LogEntry,
    NamedColor,
    Profile,
    ProfileTimer,
    ProfileTimerSubscription,
    ProfileVariable,
    RuleGroupSummary,
    Session,
    SessionProfile,
    Substitute,
    Trigger,
    Variable,
  } from './settings/types';

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
    { id: 'subs', label: 'Substitutions' },
    { id: 'highlights', label: 'Highlights' },
    { id: 'hotkeys', label: 'Hotkeys' },
    { id: 'timers', label: 'Tickers' },
    { id: 'groups', label: 'Groups' },
    { id: 'history', label: 'History' },
    { id: 'logs', label: 'Logs' },
    { id: 'app', label: 'App' },
  ];

  // Logs State
  let logEntries: LogEntry[] = [];
  let logTotal = 0;
  let logPage = 1;
  let logLimit = 100;
  let logFrom = new Date().toISOString().split('T')[0];
  let logTo = new Date().toISOString().split('T')[0];

  let searchQuery = '';
  let searchResults: LogEntry[][] = []; // Groups of entries (match + context)
  let searchCursor: number | null = null;
  let searchMode = false;
  let contextMode = false;
  let contextEntries: LogEntry[] = [];
  let contextAnchorID: number | null = null;

  // Sessions State
  let sessions: Session[] = [];
  const defaultSession = (): Partial<Session> => ({ name: '', mud_host: '', mud_port: 0, initial_commands: '', ansi_theme: 'classic', mccp_enabled: 1 });
  let sessionEditor: Partial<Session> = defaultSession();
  let editingSessionID: number | null = null;
  let editingSessionDraft: Partial<Session> = defaultSession();
  let inlineSessionError = '';
  let sessionProfileToAddID: number | undefined = undefined;
  $: currentSession = sessions.find(s => s.id === selectedSessionID);

  let sessionProfiles: SessionProfile[] = [];

  // History State
  let historyEntries: HistoryEntry[] = [];

  // Profiles State
  let profiles: Profile[] = [];
  const defaultProfile = (): Partial<Profile> => ({ name: '', description: '' });
  let profileEditor: Partial<Profile> = defaultProfile();
  let editingProfileID: number | null = null;
  let editingProfileDraft: Partial<Profile> = defaultProfile();
  let inlineProfileError = '';
  $: currentProfile = profiles.find(p => p.id === selectedProfileID);

  // Variables State (Session-scoped)
  let variables: Variable[] = [];
  const defaultVariable = (): Variable => ({ key: '', value: '' });
  let newVarKey = '';
  let newVarValue = '';
  let editingVariableKey: string | null = null;
  let editingVariableDraft: Variable = defaultVariable();
  let inlineVariableError = '';

  // Aliases State
  let aliases: Alias[] = [];
  const defaultAlias = (): Alias => ({ name: '', template: '', enabled: true, group_name: '' });
  let aliasEditor: Alias = defaultAlias();
  let editingAliasID: number | null = null;
  let editingAliasDraft: Alias = defaultAlias();
  let inlineAliasError = '';

  // Triggers State
  let triggers: Trigger[] = [];
  const defaultTrigger = (): Trigger => ({ name: '', pattern: '', command: '', enabled: true, is_button: false, stop_after_match: false, group_name: '', target_buffer: '', buffer_action: '' });
  let triggerEditor: Trigger = defaultTrigger();
  let editingTriggerID: number | null = null;
  let editingTriggerDraft: Trigger = defaultTrigger();
  let inlineTriggerError = '';

  // Highlights State
  let highlights: Highlight[] = [];
  const defaultHighlight = (): Highlight => ({ pattern: '', fg: '', bg: '', bold: false, faint: false, italic: false, underline: false, strikethrough: false, blink: false, reverse: false, enabled: true, group_name: '' });
  let highlightEditor: Highlight = defaultHighlight();
  let editingHighlightID: number | null = null;
  let editingHighlightDraft: Highlight = defaultHighlight();
  let inlineHighlightError = '';

  // Substitutions State
  let subs: Substitute[] = [];
  const defaultSub = (): Substitute => ({ pattern: '', replacement: '', is_gag: false, enabled: true, group_name: '' });
  let subEditor: Substitute = defaultSub();
  let editingSubID: number | null = null;
  let editingSubDraft: Substitute = defaultSub();
  let inlineSubError = '';
  let subPreviewText = 'foo bar';

  // Hotkeys State
  let hotkeys: Hotkey[] = [];
  const defaultHotkey = (): Hotkey => ({ shortcut: '', command: '', mobile_row: 0, mobile_order: 0 });
  let hotkeyEditor: Hotkey = defaultHotkey();

  // Profile Timers State
  let profileTimers: ProfileTimer[] = [];
  let allProfilesTimers: Record<number, ProfileTimer[]> = {};
  let expandedTimerSubs: Record<string, boolean> = {}; // key: "profileID|timerName"
  let newTimerForms: Record<number, { name: string; cycle_ms: number; icon: string; repeat_mode: string }> = {}; // per profileID
  let newSubForms: Record<string, { second: number; command: string; is_removal: boolean; is_bulk: boolean }> = {}; // key: "profileID|timerName"

  // Profile Variables State
  let profileVariables: ProfileVariable[] = [];
  const defaultProfileVariable = (): ProfileVariable => ({ name: '', default_value: '', description: '' });
  let profileVariableEditor: ProfileVariable = defaultProfileVariable();
  let editingProfileVariableID: number | null = null;
  let editingProfileVariableDraft: ProfileVariable = defaultProfileVariable();
  let inlineProfileVariableError = '';


  // Groups State
  let groups: RuleGroupSummary[] = [];

  // Profile Files State
  let profileFiles: { filename: string }[] = [];

  let namedColorList: NamedColor[] = [];
  const namedColorAliasMap: Record<string, string> = {
    'grey': 'white', 'gray': 'white', 'purple': 'magenta',
    'coal': 'black', 'charcoal': 'black', 'yellow': 'brown', 'light brown': 'brown'
  };

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

    // 1. Check named color list from backend
    const named = namedColorList.find(c => c.name === normalized);
    if (named) return named.hex;

    // 2. Check local aliases
    const aliased = namedColorAliasMap[normalized];
    if (aliased) {
      const namedAliased = namedColorList.find(c => c.name === aliased);
      if (namedAliased) return namedAliased.hex;
    }

    // 3. Parse hex
    const parsedHex = parseHexColor(normalized);
    if (parsedHex) return rgbToHex(parsedHex);

    // 4. Parse 256:N
    if (normalized.startsWith('256:')) {
      const index = Number.parseInt(normalized.slice(4), 10);
      if (!Number.isNaN(index) && index >= 0 && index <= 255) return rgbToHex(ansi256ToRGB(index));
    }
    return '#cccccc';
  }

  function namedColorSelectValue(value: string): string {
    const normalized = value.trim().toLowerCase();
    if (!normalized) return '';
    return namedColorList.some((c) => c.name === normalized) ? normalized : '__custom__';
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
      const [sessRes, profRes, colorsRes] = await Promise.all([
        fetch('/api/sessions', { headers }),
        fetch('/api/profiles', { headers }),
        fetch('/api/colors', { headers })
      ]);
      sessions = await sessRes.json() || [];
      profiles = await profRes.json() || [];
      if (colorsRes.ok) namedColorList = await colorsRes.json() || [];

      if (selectedSessionID === null && sessions.length > 0) selectedSessionID = sessions[0]!.id;
      if (selectedProfileID === null && profiles.length > 0) selectedProfileID = profiles[0]!.id;

      if (currentTab === 'app') {
        const appRes = await fetch('/api/app/settings', { headers });
        appSettings = await appRes.json();
      } else if (currentTab === 'logs' && selectedSessionID) {
        await fetchLogs();
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
      } else if (currentTab === 'timers' && profiles.length > 0) {
        const timersMap: Record<number, ProfileTimer[]> = {};
        const nextTimerForms = { ...newTimerForms };
        const nextSubForms = { ...newSubForms };

        for (const p of profiles) {
          const res = await fetch(`/api/profiles/${p.id}/timers`, { headers });
          const list = await res.json() || [];
          timersMap[p.id] = list;

          // Pre-initialize newTimerForms for each profile
          if (!nextTimerForms[p.id]) {
            nextTimerForms[p.id] = { name: '', cycle_ms: 1000, icon: '', repeat_mode: 'repeating' };
          }

          // Pre-initialize newSubForms for each timer
          for (const t of list) {
            const key = `${p.id}|${t.name}`;
            if (!nextSubForms[key]) {
              nextSubForms[key] = { second: 0, command: '', is_removal: false, is_bulk: false };
            }
          }
        }

        newTimerForms = nextTimerForms;
        newSubForms = nextSubForms;
        allProfilesTimers = timersMap;
      } else if (['aliases', 'triggers', 'subs', 'highlights', 'hotkeys', 'groups', 'declared_variables'].includes(currentTab) && selectedProfileID) {
        const endpoint = currentTab === 'declared_variables' ? 'variables' : currentTab;
        const res = await fetch(`/api/profiles/${selectedProfileID}/${endpoint}`, { headers });
        const data = await res.json() || [];
        if (currentTab === 'aliases') aliases = data;
        else if (currentTab === 'triggers') triggers = data;
        else if (currentTab === 'subs') subs = data;
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
    } else if (['aliases', 'triggers', 'subs', 'highlights', 'hotkeys', 'declared_variables'].includes(domain) && selectedProfileID) {
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

    const resp = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(body)
    });
    if (!resp.ok) {
      formError = await resp.text() || `Failed to save ${domain}.`;
      return;
    }
    resetFn();
    await fetchData();
  }

  function startInlineAliasEdit(alias: Alias) {
    if (alias.id === undefined) return;
    if (editingAliasID !== null && editingAliasID !== alias.id && !confirm('Discard unsaved alias changes?')) return;
    inlineAliasError = '';
    editingAliasID = alias.id;
    editingAliasDraft = { ...alias };
  }

  function cancelInlineAliasEdit() {
    inlineAliasError = '';
    editingAliasID = null;
    editingAliasDraft = defaultAlias();
  }

  function validateInlinePosition(label: string, item: { position?: number }): string {
    const position = Number(item.position);
    if (!Number.isInteger(position) || position <= 0) {
      return `${label} position must be a positive whole number.`;
    }
    item.position = position;
    return '';
  }

  function validateVariableKey(keyValue: string): string {
    const key = keyValue.trim();
    if (!key) return 'Variable key is required.';
    if (key.startsWith('$')) return "Variable key cannot start with '$'.";
    return '';
  }

  function startInlineVariableEdit(variable: Variable) {
    if (editingVariableKey !== null && editingVariableKey !== variable.key && !confirm('Discard unsaved variable changes?')) return;
    inlineVariableError = '';
    editingVariableKey = variable.key;
    editingVariableDraft = { ...variable };
  }

  function cancelInlineVariableEdit() {
    inlineVariableError = '';
    editingVariableKey = null;
    editingVariableDraft = defaultVariable();
  }

  async function saveInlineVariableEdit() {
    if (!selectedSessionID) return;
    const validationError = validateVariableKey(editingVariableDraft.key);
    if (validationError) { inlineVariableError = validationError; return; }

    inlineVariableError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/sessions/${selectedSessionID}/variables`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({ key: editingVariableDraft.key, value: editingVariableDraft.value }),
    });
    if (!resp.ok) {
      inlineVariableError = await resp.text() || 'Failed to save variable.';
      return;
    }
    cancelInlineVariableEdit();
    await fetchData();
  }

  async function saveInlineAliasEdit() {
    if (!selectedProfileID || editingAliasDraft.id === undefined) return;
    const positionError = validateInlinePosition('Alias', editingAliasDraft);
    if (positionError) { inlineAliasError = positionError; return; }
    const validationError = validateItem('aliases', editingAliasDraft);
    if (validationError) { inlineAliasError = validationError; return; }

    inlineAliasError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/profiles/${selectedProfileID}/aliases/${editingAliasDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingAliasDraft),
    });
    if (!resp.ok) {
      inlineAliasError = await resp.text() || 'Failed to save alias.';
      return;
    }
    cancelInlineAliasEdit();
    await fetchData();
  }

  function startInlineTriggerEdit(trigger: Trigger) {
    if (trigger.id === undefined) return;
    if (editingTriggerID !== null && editingTriggerID !== trigger.id && !confirm('Discard unsaved trigger changes?')) return;
    inlineTriggerError = '';
    editingTriggerID = trigger.id;
    editingTriggerDraft = { ...trigger };
  }

  function cancelInlineTriggerEdit() {
    inlineTriggerError = '';
    editingTriggerID = null;
    editingTriggerDraft = defaultTrigger();
  }

  async function saveInlineTriggerEdit() {
    if (!selectedProfileID || editingTriggerDraft.id === undefined) return;
    const positionError = validateInlinePosition('Trigger', editingTriggerDraft);
    if (positionError) { inlineTriggerError = positionError; return; }
    const validationError = validateItem('triggers', editingTriggerDraft);
    if (validationError) { inlineTriggerError = validationError; return; }

    inlineTriggerError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/profiles/${selectedProfileID}/triggers/${editingTriggerDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingTriggerDraft),
    });
    if (!resp.ok) {
      inlineTriggerError = await resp.text() || 'Failed to save trigger.';
      return;
    }
    cancelInlineTriggerEdit();
    await fetchData();
  }

  function startInlineSubEdit(sub: Substitute) {
    if (sub.id === undefined) return;
    if (editingSubID !== null && editingSubID !== sub.id && !confirm('Discard unsaved substitution changes?')) return;
    inlineSubError = '';
    editingSubID = sub.id;
    editingSubDraft = { ...sub };
  }

  function cancelInlineSubEdit() {
    inlineSubError = '';
    editingSubID = null;
    editingSubDraft = defaultSub();
  }

  async function saveInlineSubEdit() {
    if (!selectedProfileID || editingSubDraft.id === undefined) return;
    const positionError = validateInlinePosition('Substitution', editingSubDraft);
    if (positionError) { inlineSubError = positionError; return; }
    const validationError = validateItem('subs', editingSubDraft);
    if (validationError) { inlineSubError = validationError; return; }

    inlineSubError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/profiles/${selectedProfileID}/subs/${editingSubDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingSubDraft),
    });
    if (!resp.ok) {
      inlineSubError = await resp.text() || 'Failed to save substitution.';
      return;
    }
    cancelInlineSubEdit();
    await fetchData();
  }

  function startInlineHighlightEdit(highlight: Highlight) {
    if (highlight.id === undefined) return;
    if (editingHighlightID !== null && editingHighlightID !== highlight.id && !confirm('Discard unsaved highlight changes?')) return;
    inlineHighlightError = '';
    editingHighlightID = highlight.id;
    editingHighlightDraft = { ...highlight };
  }

  function cancelInlineHighlightEdit() {
    inlineHighlightError = '';
    editingHighlightID = null;
    editingHighlightDraft = defaultHighlight();
  }

  async function saveInlineHighlightEdit() {
    if (!selectedProfileID || editingHighlightDraft.id === undefined) return;
    const positionError = validateInlinePosition('Highlight', editingHighlightDraft);
    if (positionError) { inlineHighlightError = positionError; return; }
    const validationError = validateItem('highlights', editingHighlightDraft);
    if (validationError) { inlineHighlightError = validationError; return; }

    inlineHighlightError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/profiles/${selectedProfileID}/highlights/${editingHighlightDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingHighlightDraft),
    });
    if (!resp.ok) {
      inlineHighlightError = await resp.text() || 'Failed to save highlight.';
      return;
    }
    cancelInlineHighlightEdit();
    await fetchData();
  }

  function startInlineProfileVariableEdit(variable: ProfileVariable) {
    if (variable.id === undefined) return;
    if (editingProfileVariableID !== null && editingProfileVariableID !== variable.id && !confirm('Discard unsaved declared variable changes?')) return;
    inlineProfileVariableError = '';
    editingProfileVariableID = variable.id;
    editingProfileVariableDraft = { ...variable };
  }

  function cancelInlineProfileVariableEdit() {
    inlineProfileVariableError = '';
    editingProfileVariableID = null;
    editingProfileVariableDraft = defaultProfileVariable();
  }

  async function saveInlineProfileVariableEdit() {
    if (!selectedProfileID || editingProfileVariableDraft.id === undefined) return;
    const positionError = validateInlinePosition('Declared variable', editingProfileVariableDraft);
    if (positionError) { inlineProfileVariableError = positionError; return; }
    const validationError = validateItem('declared_variables', editingProfileVariableDraft);
    if (validationError) { inlineProfileVariableError = validationError; return; }

    inlineProfileVariableError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/profiles/${selectedProfileID}/variables/${editingProfileVariableDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingProfileVariableDraft),
    });
    if (!resp.ok) {
      inlineProfileVariableError = await resp.text() || 'Failed to save declared variable.';
      return;
    }
    cancelInlineProfileVariableEdit();
    await fetchData();
  }

  function validateItem(domain: string, item: any): string {
    if (domain === 'variables') return validateVariableKey(newVarKey);
    if (domain === 'aliases') return !item.name?.trim() ? 'Alias name is required.' : (!item.template?.trim() ? 'Alias template is required.' : '');
    if (domain === 'triggers') return !item.pattern?.trim() ? 'Trigger pattern is required.' : (!item.command?.trim() ? 'Trigger command is required.' : '');
    if (domain === 'subs') return !item.pattern?.trim() ? 'Substitution pattern is required.' : '';
    if (domain === 'highlights') return !item.pattern?.trim() ? 'Highlight pattern is required.' : '';
    if (domain === 'hotkeys') return !item.shortcut?.trim() ? 'Shortcut is required.' : (!item.command?.trim() ? 'Command is required.' : '');
    if (domain === 'declared_variables') {
      const name = item.name?.trim() || '';
      if (!name) return 'Variable name is required.';
      if (name.startsWith('$')) return "Variable name cannot start with '$'.";
      return '';
    }
    if (domain === 'sessions') return !item.name?.trim() ? 'Session name is required.' : (!item.mud_host?.trim() ? 'MUD host is required.' : (!item.mud_port ? 'MUD port is required.' : ''));
    if (domain === 'profiles') return !item.name?.trim() ? 'Profile name is required.' : '';
    return '';
  }

  function startInlineProfileEdit(profile: Profile) {
    if (editingProfileID !== null && editingProfileID !== profile.id && !confirm('Discard unsaved profile changes?')) return;
    inlineProfileError = '';
    editingProfileID = profile.id;
    editingProfileDraft = { ...profile };
  }

  function cancelInlineProfileEdit() {
    inlineProfileError = '';
    editingProfileID = null;
    editingProfileDraft = defaultProfile();
  }

  async function saveInlineProfileEdit() {
    if (editingProfileDraft.id === undefined) return;
    const validationError = validateItem('profiles', editingProfileDraft);
    if (validationError) { inlineProfileError = validationError; return; }

    inlineProfileError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/profiles/${editingProfileDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingProfileDraft),
    });
    if (!resp.ok) {
      inlineProfileError = await resp.text() || 'Failed to save profile.';
      return;
    }
    cancelInlineProfileEdit();
    await fetchData();
  }

  function startInlineSessionEdit(session: Session) {
    if (editingSessionID !== null && editingSessionID !== session.id && !confirm('Discard unsaved session changes?')) return;
    inlineSessionError = '';
    editingSessionID = session.id;
    editingSessionDraft = { ...session, ansi_theme: session.ansi_theme || 'classic' };
  }

  function cancelInlineSessionEdit() {
    inlineSessionError = '';
    editingSessionID = null;
    editingSessionDraft = defaultSession();
  }

  async function saveInlineSessionEdit() {
    if (editingSessionDraft.id === undefined) return;
    const validationError = validateItem('sessions', editingSessionDraft);
    if (validationError) { inlineSessionError = validationError; return; }

    inlineSessionError = '';
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const resp = await fetch(`/api/sessions/${editingSessionDraft.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify(editingSessionDraft),
    });
    if (!resp.ok) {
      inlineSessionError = await resp.text() || 'Failed to save session.';
      return;
    }
    cancelInlineSessionEdit();
    await fetchData();
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
    } else if (['aliases', 'triggers', 'subs', 'highlights', 'hotkeys', 'declared_variables'].includes(domain) && selectedProfileID) {
        const endpoint = domain === 'declared_variables' ? 'variables' : domain;
        url = `/api/profiles/${selectedProfileID}/${endpoint}/${id}`;
    }
    const resp = await fetch(url, { method: 'DELETE', headers: { 'X-Session-Token': token } });
    if (!resp.ok) {
      formError = await resp.text() || `Failed to delete ${domain.slice(0, -1)}.`;
      return;
    }
    await fetchData();
  }

  async function toggleItem(domain: 'aliases' | 'triggers' | 'subs' | 'highlights', item: any, enabled: boolean) {
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

  async function saveProfileTimer(profileID: number, timer: any) {
    if (!timer.name || !timer.name.trim()) {
      alert("Ticker name is required");
      return;
    }
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch(`/api/profiles/${profileID}/timers`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({
        profile_id: profileID,
        name: timer.name,
        icon: timer.icon || '',
        cycle_ms: parseInt(String(timer.cycle_ms), 10) || 1000,
        repeat_mode: timer.repeat_mode || 'repeating'
      }),
    });
    if (!res.ok) {
      alert("Failed to save ticker: " + await res.text());
    } else {
      await fetchData();
    }
  }

  async function deleteProfileTimer(profileID: number, name: string) {
    if (!confirm(`Delete ticker "${name}"? This will also delete all its subscriptions.`)) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch(`/api/profiles/${profileID}/timers/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      headers: { 'X-Session-Token': token },
    });
    if (!res.ok) {
      alert("Failed to delete ticker: " + await res.text());
    } else {
      await fetchData();
    }
  }

  async function saveProfileTimerSubscription(profileID: number, timerName: string, sub: any) {
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch(`/api/profiles/${profileID}/timers/${encodeURIComponent(timerName)}/subscriptions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': token },
      body: JSON.stringify({
        profile_id: profileID,
        timer_name: timerName,
        second: parseInt(String(sub.second), 10) || 0,
        sort_order: sub.sort_order || 0,
        command: sub.command || '',
        is_removal: !!sub.is_removal,
        is_bulk: !!sub.is_bulk
      }),
    });
    if (!res.ok) {
      alert("Failed to save subscription: " + await res.text());
    } else {
      await fetchData();
    }
  }

  async function deleteProfileTimerSubscription(profileID: number, timerName: string, sub: any) {
    if (!confirm(`Delete subscription for second ${sub.second}?`)) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const res = await fetch(`/api/profiles/${profileID}/timers/${encodeURIComponent(timerName)}/subscriptions/${sub.second}/${sub.sort_order}`, {
      method: 'DELETE',
      headers: { 'X-Session-Token': token },
    });
    if (!res.ok) {
      alert("Failed to delete subscription: " + await res.text());
    } else {
      await fetchData();
    }
  }

  function getNewSubForm(profileID: number, timerName: string) {
    const key = `${profileID}|${timerName}`;
    if (!newSubForms[key]) {
      newSubForms = { ...newSubForms, [key]: { second: 0, command: '', is_removal: false, is_bulk: false } };
    }
    return newSubForms[key];
  }

  async function addSub(profileID: number, timerName: string) {
    const sub = getNewSubForm(profileID, timerName);
    // Calculate sort_order: find max sort_order for the same second in existing subs
    const existingSubs = (allProfilesTimers[profileID] || [])
      .find(t => t.name === timerName)?.subscriptions || [];
    let maxSort = -1;
    for (const e of existingSubs) {
      if (e.second === sub.second && e.sort_order > maxSort) {
        maxSort = e.sort_order;
      }
    }
    const subWithOrder = { ...sub, sort_order: maxSort + 1 };
    await saveProfileTimerSubscription(profileID, timerName, subWithOrder);
    newSubForms = { ...newSubForms, [`${profileID}|${timerName}`]: { second: 0, command: '', is_removal: false, is_bulk: false } };
  }

  function getNewTimerForm(profileID: number) {
    if (!newTimerForms[profileID]) {
      newTimerForms = { ...newTimerForms, [profileID]: { name: '', cycle_ms: 1000, icon: '', repeat_mode: 'repeating' } };
    }
    return newTimerForms[profileID];
  }

  async function addTimer(profileID: number) {
    const timer = getNewTimerForm(profileID);
    if (!timer.name || !timer.name.trim()) {
      alert("Ticker name is required");
      return;
    }
    await saveProfileTimer(profileID, timer);
    newTimerForms = { ...newTimerForms, [profileID]: { name: '', cycle_ms: 1000, icon: '', repeat_mode: 'repeating' } };
  }

  async function sessionAction(action: 'connect' | 'disconnect', id: number) {
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/sessions/${id}/${action}`, { method: 'POST', headers: { 'X-Session-Token': token } });
    await fetchData();
  }

  async function fetchLogs() {
    if (!selectedSessionID) return;
    loading = true;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const headers = { 'X-Session-Token': token };

    const from = logFrom ? new Date(logFrom).toISOString() : '';
    const to = logTo ? new Date(logTo + 'T23:59:59Z').toISOString() : '';

    const params = new URLSearchParams({
        from,
        to,
        page: String(logPage),
        limit: String(logLimit)
    });

    try {
        const res = await fetch(`/api/sessions/${selectedSessionID}/logs?${params}`, { headers });
        const data = await res.json();
        logEntries = data.entries || [];
        logTotal = data.total || 0;
    } catch (e) {
        console.error('Failed to fetch logs', e);
    } finally {
        loading = false;
    }
  }

  async function searchLogs(loadMore = false) {
    if (!selectedSessionID || !searchQuery) return;
    loading = true;
    searchMode = true;
    contextMode = false;

    if (!loadMore) {
        searchResults = [];
        searchCursor = null;
        logEntries = []; // Clear browsing entries too to avoid confusion
    }

    // @ts-ignore
    const token = window.API_TOKEN || '';
    const headers = { 'X-Session-Token': token };

    const url = `/api/sessions/${selectedSessionID}/logs/search?q=${encodeURIComponent(searchQuery)}` +
                (loadMore && searchCursor ? `&before_id=${searchCursor}` : '');

    try {
        const res = await fetch(url, { headers });
        const data = await res.json() || {};
        const newGroups = data.groups || [];
        if (loadMore) {
            searchResults = [...searchResults, ...newGroups];
        } else {
            searchResults = newGroups;
        }
        searchCursor = data.cursor || null;
    } catch (e) {
        console.error('Search failed', e);
    } finally {
        loading = false;
    }
  }

  async function showContext(entryID: number) {
    if (!selectedSessionID) return;
    loading = true;
    contextMode = true;
    searchMode = false;
    contextAnchorID = entryID;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const headers = { 'X-Session-Token': token };
    try {
        const res = await fetch(`/api/sessions/${selectedSessionID}/logs/${entryID}/context?before=20&after=20`, { headers });
        contextEntries = await res.json() || [];
    } catch (e) {
        console.error('Failed to fetch context', e);
    } finally {
        loading = false;
    }
  }

  async function loadMoreContext(direction: 'above' | 'below') {
    if (!selectedSessionID || contextEntries.length === 0) return;
    const anchor = direction === 'above' ? contextEntries[0] : contextEntries[contextEntries.length - 1];
    if (!anchor) return;

    // @ts-ignore
    const token = window.API_TOKEN || '';
    const headers = { 'X-Session-Token': token };
    const query = direction === 'above' ? `before=50&after=0` : `before=0&after=50`;

    try {
        const res = await fetch(`/api/sessions/${selectedSessionID}/logs/${anchor.id}/context?${query}`, { headers });
        const data: LogEntry[] = await res.json() || [];
        if (direction === 'above') {
            contextEntries = [...data.filter(e => e.id !== anchor.id), ...contextEntries];
        } else {
            contextEntries = [...contextEntries, ...data.filter(e => e.id !== anchor.id)];
        }
    } catch (e) {
        console.error('Failed to load more context', e);
    }
  }

  function downloadLogs() {
    if (!selectedSessionID) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const from = logFrom ? new Date(logFrom).toISOString() : '';
    const to = logTo ? new Date(logTo + 'T23:59:59Z').toISOString() : '';

    const params = new URLSearchParams({
        from,
        to,
        token
    });
    window.open(`/api/sessions/${selectedSessionID}/logs/download?${params}`, '_blank');
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
    else if (domain === 'subs') list = subs;
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

  function formatTimerCycle(ms: number): string {
    const seconds = Math.round(ms / 1000);
    if (seconds >= 60) {
      const minutes = Math.floor(seconds / 60);
      const rest = seconds % 60;
      return `${minutes}:${String(rest).padStart(2, '0')}`;
    }
    return `${seconds}s`;
  }

  function ruleCountForGroup(domain: string, groupName: string): number {
    const normalized = groupName || 'default';
    if (domain === 'aliases') return aliases.filter((item) => (item.group_name || 'default') === normalized).length;
    if (domain === 'triggers') return triggers.filter((item) => (item.group_name || 'default') === normalized).length;
    if (domain === 'subs') return subs.filter((item) => (item.group_name || 'default') === normalized).length;
    if (domain === 'highlights') return highlights.filter((item) => (item.group_name || 'default') === normalized).length;
    return 0;
  }

  function previewSubstitution(rule: Substitute): string {
    if (!rule.pattern.trim()) return subPreviewText;
    try {
      const re = new RegExp(rule.pattern, 'g');
      if (!re.test(subPreviewText)) return subPreviewText;
      re.lastIndex = 0;
      if (rule.is_gag) return '(gagged)';
      return subPreviewText.replace(re, (...args: any[]) => {
        const maybeGroups = args[args.length - 1];
        const captures = typeof maybeGroups === 'object' ? args.slice(0, -3) : args.slice(0, -2);
        return rule.replacement.replace(/%(\d+)/g, (_match, index) => captures[Number(index)] ?? '');
      });
    } catch (_e) {
      return '(invalid regex)';
    }
  }

  $: {
    const fetchKey = `${currentTab}|${selectedSessionID ?? ''}|${selectedProfileID ?? ''}`;
    if (lastFetchKey !== null && fetchKey !== lastFetchKey) {
      lastFetchKey = fetchKey;
      void fetchData();
    }
  }

  let monitoringInterval: number | null = null;

  onMount(() => {
    wakeLockEnabled = localStorage.getItem(wakeLockEnabledStorageKey) !== 'false';
    lastFetchKey = `${currentTab}|${selectedSessionID ?? ''}|${selectedProfileID ?? ''}`;
    void fetchData();

    // Small polling refresh for manual testing on the sessions page
    monitoringInterval = window.setInterval(() => {
      if (currentTab === 'sessions' && !loading) {
        void fetchData();
      }
    }, 1500);
  });

  onDestroy(() => {
    if (monitoringInterval) {
      clearInterval(monitoringInterval);
    }
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

    {#if ['aliases', 'triggers', 'subs', 'highlights', 'groups', 'hotkeys', 'declared_variables'].includes(currentTab)}
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
      <VariablesSection
        currentSession={currentSession}
        {formError}
        {variables}
        bind:newVarKey
        bind:newVarValue
        {editingVariableKey}
        bind:editingVariableDraft
        {inlineVariableError}
        addVariable={() => saveItem('variables', {}, () => { newVarKey = ''; newVarValue = ''; })}
        {startInlineVariableEdit}
        deleteVariable={(key) => deleteItem('variables', key)}
        {saveInlineVariableEdit}
        {cancelInlineVariableEdit}
      />

    {:else if currentTab === 'declared_variables'}
      <DeclaredVariablesSection
        currentProfile={currentProfile}
        {formError}
        {profileVariables}
        bind:profileVariableEditor
        {editingProfileVariableID}
        bind:editingProfileVariableDraft
        {inlineProfileVariableError}
        addProfileVariable={() => saveItem('declared_variables', { ...profileVariableEditor, id: undefined, position: undefined }, () => profileVariableEditor = defaultProfileVariable())}
        moveProfileVariable={(variable, direction) => moveRule('declared_variables', variable, direction)}
        {startInlineProfileVariableEdit}
        deleteProfileVariable={(id) => deleteItem('declared_variables', id)}
        {saveInlineProfileVariableEdit}
        {cancelInlineProfileVariableEdit}
      />

    {:else if currentTab === 'aliases'}
      <AliasesSection
        currentProfile={currentProfile}
        {formError}
        {aliases}
        bind:aliasEditor
        {editingAliasID}
        bind:editingAliasDraft
        {inlineAliasError}
        addAlias={() => saveItem('aliases', { ...aliasEditor, id: undefined, position: undefined }, () => aliasEditor = defaultAlias())}
        moveAlias={(alias, direction) => moveRule('aliases', alias, direction)}
        toggleAlias={(alias, enabled) => toggleItem('aliases', alias, enabled)}
        {startInlineAliasEdit}
        deleteAlias={(id) => deleteItem('aliases', id)}
        {saveInlineAliasEdit}
        {cancelInlineAliasEdit}
      />

    {:else if currentTab === 'triggers'}
      <TriggersSection
        currentProfile={currentProfile}
        {formError}
        {triggers}
        bind:triggerEditor
        {editingTriggerID}
        bind:editingTriggerDraft
        {inlineTriggerError}
        addTrigger={() => saveItem('triggers', { ...triggerEditor, id: undefined, position: undefined }, () => triggerEditor = defaultTrigger())}
        moveTrigger={(trigger, direction) => moveRule('triggers', trigger, direction)}
        toggleTrigger={(trigger, enabled) => toggleItem('triggers', trigger, enabled)}
        {startInlineTriggerEdit}
        deleteTrigger={(id) => deleteItem('triggers', id)}
        {saveInlineTriggerEdit}
        {cancelInlineTriggerEdit}
      />

    {:else if currentTab === 'subs'}
      <SubstitutionsSection
        currentProfile={currentProfile}
        {formError}
        {subs}
        bind:subEditor
        bind:subPreviewText
        {editingSubID}
        bind:editingSubDraft
        {inlineSubError}
        {previewSubstitution}
        addSubstitution={() => saveItem('subs', { ...subEditor, id: undefined, position: undefined }, () => subEditor = defaultSub())}
        moveSubstitution={(sub, direction) => moveRule('subs', sub, direction)}
        toggleSubstitution={(sub, enabled) => toggleItem('subs', sub, enabled)}
        {startInlineSubEdit}
        deleteSubstitution={(id) => deleteItem('subs', id)}
        {saveInlineSubEdit}
        {cancelInlineSubEdit}
      />

    {:else if currentTab === 'highlights'}
      <HighlightsSection
        currentProfile={currentProfile}
        {formError}
        {highlights}
        bind:highlightEditor
        {editingHighlightID}
        bind:editingHighlightDraft
        {inlineHighlightError}
        {namedColorList}
        {namedColorSelectValue}
        {colorValueToHex}
        {colorValueToCss}
        {setHighlightColor}
        addHighlight={() => saveItem('highlights', { ...highlightEditor, id: undefined, position: undefined }, () => highlightEditor = defaultHighlight())}
        moveHighlight={(highlight, direction) => moveRule('highlights', highlight, direction)}
        toggleHighlight={(highlight, enabled) => toggleItem('highlights', highlight, enabled)}
        {startInlineHighlightEdit}
        deleteHighlight={(id) => deleteItem('highlights', id)}
        {saveInlineHighlightEdit}
        {cancelInlineHighlightEdit}
      />

    {:else if currentTab === 'hotkeys'}
      <HotkeysSection
        currentProfile={currentProfile}
        {formError}
        {hotkeys}
        bind:hotkeyEditor
        saveHotkey={() => saveItem('hotkeys', hotkeyEditor, () => hotkeyEditor = defaultHotkey())}
        moveHotkey={(hotkey, direction) => moveRule('hotkeys', hotkey, direction)}
        editHotkey={(hotkey) => hotkeyEditor = { ...hotkey }}
        deleteHotkey={(id) => deleteItem('hotkeys', id)}
      />

    {:else if currentTab === 'timers'}
      <TimersSection
        {profiles}
        {allProfilesTimers}
        bind:expandedTimerSubs
        bind:newTimerForms
        bind:newSubForms
        {saveProfileTimer}
        {deleteProfileTimer}
        {saveProfileTimerSubscription}
        {deleteProfileTimerSubscription}
        {addSub}
        {addTimer}
      />

    {:else if currentTab === 'groups'}
      <GroupsSection
        currentProfile={currentProfile}
        {formError}
        {groups}
        {titleCase}
        {ruleCountForGroup}
        {toggleGroup}
      />

    {:else if currentTab === 'profiles'}
      <ProfilesSection
        {formError}
        {profiles}
        bind:profileEditor
        {profileFiles}
        {editingProfileID}
        bind:editingProfileDraft
        {inlineProfileError}
        addProfile={() => saveItem('profiles', { ...profileEditor, id: undefined }, () => profileEditor = defaultProfile())}
        {exportAllProfiles}
        {importAllProfiles}
        {exportProfile}
        {importProfileFromFile}
        {startInlineProfileEdit}
        deleteProfile={(id) => deleteItem('profiles', id)}
        {saveInlineProfileEdit}
        {cancelInlineProfileEdit}
      />

    {:else if currentTab === 'sessions'}
      <SessionsSection
        {formError}
        {sessions}
        bind:sessionEditor
        {editingSessionID}
        bind:editingSessionDraft
        {inlineSessionError}
        {currentSession}
        {sessionProfiles}
        {profiles}
        bind:sessionProfileToAddID
        addSession={() => saveItem('sessions', { ...sessionEditor, id: undefined }, () => sessionEditor = defaultSession())}
        {sessionAction}
        viewSession={(id) => { selectedSessionID = id; currentTab = 'variables'; }}
        {startInlineSessionEdit}
        deleteSession={(id) => deleteItem('sessions', id)}
        {saveInlineSessionEdit}
        {cancelInlineSessionEdit}
        {moveSessionProfile}
        {removeProfileFromSession}
        {addProfileToSession}
      />

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

    {:else if currentTab === 'logs'}
      <header class="content-header">
          <h2>Logs</h2>
          <p class="description">Browse and download historical logs for {currentSession?.name || 'selected session'}.</p>
      </header>

      <div class="editor-box">
          <div class="form-row" style="margin-bottom: 1rem;">
              <input type="text" placeholder="Search logs..." bind:value={searchQuery} on:keydown={(e) => e.key === 'Enter' && searchLogs()} />
              <button class="btn-primary" on:click={() => searchLogs()} disabled={!searchQuery || loading}>
                {loading ? 'Searching...' : 'Search'}
              </button>
              {#if searchMode || contextMode}
                <button class="btn-secondary" on:click={() => { searchMode = false; contextMode = false; fetchLogs(); }}>Back to Browsing</button>
              {/if}
          </div>
          {#if loading && searchMode && searchResults.length === 0}
            <div style="text-align: center; color: #3498db; margin-bottom: 1rem;">Searching logs for "{searchQuery}"...</div>
          {/if}
          {#if searchMode}
            <div style="font-size: 0.8rem; color: #9ba3af; margin-bottom: 1rem; text-align: center;">
                💡 Search works across all historical data, ignoring date filters.
            </div>
          {/if}
          <div class="form-row">
              <div class="form-group">
                  <label for="log-from">From</label>
                  <input id="log-from" type="date" bind:value={logFrom} on:change={() => { logPage = 1; searchMode = false; contextMode = false; fetchLogs(); }} />
              </div>
              <div class="form-group">
                  <label for="log-to">To</label>
                  <input id="log-to" type="date" bind:value={logTo} on:change={() => { logPage = 1; searchMode = false; contextMode = false; fetchLogs(); }} />
              </div>
              <div class="form-group" style="align-self: flex-end;">
                  <button class="btn-secondary" on:click={downloadLogs}>Download (.txt)</button>
              </div>
          </div>
      </div>

      {#if contextMode}
        <button class="load-more-btn" on:click={() => loadMoreContext('above')}>Load more above...</button>
        <div class="log-container">
            {#each contextEntries as entry}
                <div class="log-line {entry.id === contextAnchorID ? 'context-anchor-highlight' : ''}">
                    <div class="log-content">{entry.display_plain || entry.plain_text}</div>
                    <div class="log-meta">
                        <span>{entry.buffer}</span>
                        <span title={entry.created_at}>{new Date(entry.created_at).toLocaleString()}</span>
                    </div>
                </div>
            {/each}
        </div>
        <button class="load-more-btn" on:click={() => loadMoreContext('below')}>Load more below...</button>

      {:else if searchMode}
        {#each searchResults as group}
            {@const matchEntry = group.find(e => searchQuery && (e.display_plain || e.plain_text).toLowerCase().includes(searchQuery.toLowerCase())) || group[Math.floor(group.length/2)]}
            {@const groupDate = new Date(group[0]!.created_at)}
            <div class="editor-box" style="padding: 0; overflow: hidden; margin-bottom: 1rem;">
                <div class="search-group-header">
                    <div>
                        <span style="font-weight: bold; color: #e8edf2; margin-right: 12px;">
                            {groupDate.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })}
                            {groupDate.toLocaleTimeString()}
                        </span>
                        <span style="color: #9ba3af; margin-right: 12px;">(Match +-5 lines)</span>
                        <button class="btn-link" style="font-size: 0.8rem; font-weight: bold;" on:click={() => showContext(matchEntry!.id)}>[View Context]</button>
                    </div>
                </div>
                <div class="log-container" style="border: none; border-radius: 0;">
                    {#each group as entry}
                        {@const isMatch = searchQuery && (entry.display_plain || entry.plain_text).toLowerCase().includes(searchQuery.toLowerCase())}
                        <div class="log-line {isMatch ? 'search-match-line' : ''}">
                            <div class="log-content">
                                {#if isMatch}
                                    {@const text = entry.display_plain || entry.plain_text}
                                    {@const idx = text.toLowerCase().indexOf(searchQuery.toLowerCase())}
                                    {text.slice(0, idx)}<span class="search-match-highlight">{text.slice(idx, idx + searchQuery.length)}</span>{text.slice(idx + searchQuery.length)}
                                {:else}
                                    {entry.display_plain || entry.plain_text}
                                {/if}
                            </div>
                            <div class="log-meta">
                                <span>{entry.buffer}</span>
                                <span>{new Date(entry.created_at).toLocaleTimeString()}</span>
                            </div>
                        </div>
                    {/each}
                </div>
            </div>
        {/each}
        {#if searchResults.length === 0}
            <div class="dim-cell" style="text-align: center; padding: 2rem;">No matches found for "{searchQuery}".</div>
        {:else}
            <button class="load-more-btn" on:click={() => searchLogs(true)}>Load more search results...</button>
        {/if}

      {:else}
        <div class="log-container">
            {#each logEntries as entry}
                <div class="log-line">
                    <div class="log-content">{entry.display_plain || entry.plain_text}</div>
                    <div class="log-meta">
                        <span>{entry.buffer}</span>
                        <span title={entry.created_at}>{new Date(entry.created_at).toLocaleString()}</span>
                    </div>
                </div>
            {/each}
            {#if logEntries.length === 0}
                <div style="padding: 2rem; text-align: center; color: #484f58;">No logs found for this period.</div>
            {/if}
        </div>

        {#if logTotal > logLimit}
            <div class="form-row" style="margin-top: 1rem; justify-content: center; align-items: center; gap: 1rem;">
                <button class="btn-link" disabled={logPage <= 1} on:click={() => { logPage--; fetchLogs(); }}>Previous</button>
                <span>Page {logPage} of {Math.ceil(logTotal / logLimit)} ({logTotal} total)</span>
                <button class="btn-link" disabled={logPage >= Math.ceil(logTotal / logLimit)} on:click={() => { logPage++; fetchLogs(); }}>Next</button>
            </div>
        {/if}
      {/if}

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
  .editor-box { background: #1a1d21; padding: 20px; border-radius: 8px; border: 1px solid #2d333b; margin-bottom: 32px; }
  .selector-box { background: #1a1d21; padding: 12px 20px; border-radius: 8px; border: 1px solid #2d333b; margin-bottom: 24px; display: flex; align-items: center; gap: 12px; }
  .selector-box select { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 6px 12px; border-radius: 6px; }
  .form-row { display: flex; gap: 12px; align-items: center; }
  input[type="text"] { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; flex: 1; }
  .checkbox-label { display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: #9ba3af; cursor: pointer; }
  .btn-primary { background: #3498db; color: white; border: none; padding: 8px 20px; border-radius: 6px; cursor: pointer; }
  .btn-primary:hover { background: #2980b9; }
  .btn-secondary { background: #30363d; color: #e8edf2; border: 1px solid #30363d; padding: 8px 20px; border-radius: 6px; cursor: pointer; }
  .btn-secondary:hover { background: #3c444d; }
  .btn-link { background: none; border: none; color: #3498db; cursor: pointer; padding: 0; font-size: 0.9rem; }
  .btn-link:hover { text-decoration: underline; }
  .btn-link:disabled { color: #484f58; cursor: not-allowed; text-decoration: none; }
  .btn-danger { color: #ff7b72; }
  .btn-danger:hover { color: #f85149; }
  .search-match-highlight { background: rgba(52, 152, 219, 0.3); border-radius: 2px; }
  .context-anchor-highlight { border-left: 4px solid #3498db; padding-left: 8px; background: rgba(52, 152, 219, 0.1); }
  .search-group-header { padding: 8px 12px; background: #24292f; border-bottom: 1px solid #30363d; font-size: 0.8rem; color: #9ba3af; display: flex; justify-content: space-between; }
  .load-more-btn { width: 100%; padding: 8px; background: #1a1d21; border: 1px dashed #30363d; color: #3498db; cursor: pointer; border-radius: 4px; margin: 4px 0; font-size: 0.8rem; }
  .load-more-btn:hover { background: #24292f; }
  .btn-primary:disabled { background: #2980b9; opacity: 0.5; cursor: not-allowed; }

  .log-container { display: flex; flex-direction: column; background: #0d1117; border: 1px solid #30363d; border-radius: 6px; overflow: hidden; }
  .log-line { display: flex; justify-content: space-between; padding: 1px 12px; min-height: 1.2em; line-height: 1.2; font-family: monospace; border-left: 4px solid transparent; }
  .log-line:hover { background: rgba(255, 255, 255, 0.03); }
  .log-content { white-space: pre-wrap; word-break: break-all; flex: 1; font-size: 0.9rem; }
  .log-meta { display: flex; gap: 12px; font-size: 0.75rem; color: #484f58; white-space: nowrap; align-items: flex-start; margin-left: 16px; opacity: 0.6; }
  .log-meta:hover { opacity: 1; }
  .log-line.context-anchor-highlight { background: rgba(52, 152, 219, 0.1); border-left-color: #3498db; }
  .log-line.search-match-line { background: rgba(52, 152, 219, 0.05); }

  .data-table { width: 100%; border-collapse: collapse; }
  .data-table th { text-align: left; padding: 12px; border-bottom: 2px solid #30363d; color: #9ba3af; font-size: 0.8rem; text-transform: uppercase; }
  .data-table td { padding: 12px; border-bottom: 1px solid #21262d; font-size: 0.9rem; vertical-align: middle; }
  .key-cell { font-family: monospace; color: #f39c12; font-weight: 600; }
  .dim-cell { color: #666; font-size: 0.8rem; }
  .actions-cell { white-space: nowrap; }
  .btn-link { background: none; border: none; color: #3498db; cursor: pointer; padding: 0; font-size: 0.9rem; }
  .btn-link:hover { text-decoration: underline; }
  .btn-danger { color: #e74c3c; margin-left: 12px; }

  @media (max-width: 768px) {
    .settings-layout { grid-template-columns: 128px 1fr; height: 100svh; }
    .sidebar-header { padding: 16px 12px; }
    .sidebar-header h1 { margin: 0; font-size: 1rem; }
    .nav-links { padding: 8px 0; }
    .nav-links li button { padding: 10px 12px; font-size: 0.85rem; }
    .content { padding: 18px 14px; }
    .description { margin-bottom: 20px; }
  }

</style>
