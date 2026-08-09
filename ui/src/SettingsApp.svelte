<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
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
  import MapperProfilesSection from './settings/MapperProfilesSection.svelte';
  import MapSetsSection from './settings/MapSetsSection.svelte';
  import * as api from './settings/api';
  import type { MCPSettings, MCPSettingsWriteResponse, WebAuthSettings } from './settings/api';
  import {
    copyOfMapperProfile,
    emptyMapperProfileForm,
    formToSpec,
    specToForm,
  } from './settings/mapperProfileForm';
  import type { MapperProfileForm } from './settings/mapperProfileForm';
  import { MapperReplayRequest } from './settings/mapperReplayRequest';
  import { localDateTimeISO } from './settings/logRange';
  import { normalizeAnsiTheme } from './ansi';
  import { duplicateHotkeyShortcutError } from './settings/hotkeyValidation';
  import type {
    Alias,
    Highlight,
    HistoryEntry,
    Hotkey,
    LogEntry,
    MapperProfile,
    MapperReplayResult,
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

  let appSettings: { api_token: string; allow_exec_command: boolean; allow_webfetch_command: boolean } = { api_token: '', allow_exec_command: false, allow_webfetch_command: false };
  let mcpSettings: MCPSettings = { enabled: false, requested_enabled: false, effective_enabled: false, token_configured: false, externally_managed: false, enabled_externally_managed: false, token_externally_managed: false, listener_exposure: 'loopback' };
  let mcpOneTimeToken = '';
  let mcpBusy = false;
  let mcpRevision = 0;
  let mcpCopyStatus = '';
  let mcpTokenInput: HTMLInputElement;
  let webAuthSettings: WebAuthSettings = { enabled: false, requested_enabled: false, effective_enabled: false, username: '', credential_configured: false, externally_managed: false, current_listener: '127.0.0.1:8080', current_exposure: 'loopback', configured_listener: '127.0.0.1:8080', configured_exposure: 'loopback', listener_externally_managed: false, restart_required: false };
  let webAuthUsername = '';
  let webAuthPassword = '';
  let webAuthPasswordConfirm = '';
  let webAuthListenerMode: 'local' | 'network' = 'local';
  let webAuthListenAddr = '0.0.0.0:8080';
  let webAuthBusy = false;
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

  // A one-time credential belongs only to the response view which received it.
  $: if (currentTab !== 'app' && mcpOneTimeToken) closeMCPToken();

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
    { id: 'room_formats', label: 'Room Formats' },
    { id: 'map_sets', label: 'Map Sets' },
    { id: 'history', label: 'History' },
    { id: 'logs', label: 'Logs' },
    { id: 'app', label: 'App' },
  ];

  // Logs State
  let logEntries: LogEntry[] = [];
  let logTotal = 0;
  let logPage = 1;
  let logLimit = 100;
  // Logs tab state is mirrored into the URL query (?from&to&q) so a refresh
  // restores the filter and tabs are shareable/openable. Seed from the URL here.
  const logsURLParams = new URLSearchParams(window.location.search);
  const _today = new Date().toISOString().split('T')[0];

  // Log range (datetime-local "YYYY-MM-DDTHH:mm", to-the-minute for a single
  // raid/event). Drives BOTH the browse preview list AND the .txt/.html exports
  // — "what you see is what you export". Search is separate and ignores this
  // range (it searches the whole session history).
  let logFrom = logsURLParams.get('from') || `${_today}T00:00`;
  let logTo = logsURLParams.get('to') || `${_today}T23:59`;

  // Colored HTML export: include sent commands toggle.
  let exportIncludeCommands = true;

  let searchQuery = logsURLParams.get('q') || '';
  let didAutoSearchFromURL = false; // one-shot: auto-run ?q= search on first load
  let searchResults: LogEntry[][] = []; // Groups of entries (match + context)
  let searchCursor: number | null = null;
  let searchMode = false;
  let contextMode = false;
  let contextEntries: LogEntry[] = [];
  let contextAnchorID: number | null = null;

  // Sessions State
  let sessions: Session[] = [];
  const defaultSession = (): Partial<Session> => ({ name: '', mud_host: '', mud_port: 0, initial_commands: '', ansi_theme: 'classic', timezone: 'UTC', tz_follow: 1, mccp_enabled: 1 });
  let sessionEditor: Partial<Session> = defaultSession();
  let editingSessionID: number | null = null;
  let editingSessionDraft: Partial<Session> = defaultSession();
  let inlineSessionError = '';
  let sessionProfileToAddID: number | undefined = undefined;
  $: currentSession = sessions.find(s => s.id === selectedSessionID);

  let sessionProfiles: SessionProfile[] = [];

  // History State
  let historyEntries: HistoryEntry[] = [];
  let historyHasMore = false;
  let historyNextBeforeID: number | null = null;
  let historyKindFilter = '';
  let historyQuery = '';
  let historyQueryDraft = '';
  let historyLoading = false;
  const historyLimit = 100;
  const historyKindOptions = [
    { value: '', label: 'All kinds' },
    { value: 'input', label: 'Raw input' },
    { value: 'expanded', label: 'Sent from input' },
    { value: 'trigger', label: 'Trigger sends' },
    { value: 'key', label: 'Hotkey sends' },
    { value: 'button', label: 'Button sends' },
    { value: 'scheduler', label: 'Scheduled sends' },
    { value: 'connect', label: 'Connect sends' },
  ];
  const historyKindLabels: Record<string, string> = Object.fromEntries(historyKindOptions.map(option => [option.value, option.label]));

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
  let editingHotkeyID: number | null = null;
  let editingHotkeyDraft: Hotkey = defaultHotkey();
  let inlineHotkeyError = '';

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

  // Room Formats State ("mapper profiles" — how a room block is parsed out of
  // the MUD's output). Unrelated to the `profiles` above.
  let mapperProfiles: MapperProfile[] = [];
  let mapperForm: MapperProfileForm = emptyMapperProfileForm();
  let editingMapperProfileID: number | null = null;
  let mapperProfileError = '';
  let mapperProfileValidated = false;
  let mapperReplaySessionID: number | null = null;
  let mapperReplayLimit = 1000;
  const mapperReplayRequest = new MapperReplayRequest();
  let mapperReplayState = mapperReplayRequest.state();

  function clearMapperReplay() {
    mapperReplayState = mapperReplayRequest.clear();
  }

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
    try {
      const [sessData, profData, colorsData] = await Promise.all([
        api.fetchSessions(),
        api.fetchProfiles(),
        api.fetchColors().catch(() => [])
      ]);
      sessions = sessData || [];
      profiles = profData || [];
      namedColorList = colorsData || [];

      if (selectedSessionID === null && sessions.length > 0) selectedSessionID = sessions[0]!.id;
      if (mapperReplaySessionID === null && sessions.length > 0) mapperReplaySessionID = sessions[0]!.id;
      if (selectedProfileID === null && profiles.length > 0) selectedProfileID = profiles[0]!.id;

      if (currentTab === 'app') {
        const revision = mcpRevision;
        const [nextAppSettings, nextMCPSettings, nextWebAuthSettings] = await Promise.all([api.fetchAppSettings(), api.fetchMCPSettings(), api.fetchWebAuthSettings()]);
        appSettings = nextAppSettings;
        if (revision === mcpRevision) mcpSettings = nextMCPSettings;
        webAuthSettings = nextWebAuthSettings;
        webAuthUsername = nextWebAuthSettings.username;
        webAuthListenerMode = nextWebAuthSettings.configured_exposure === 'network' ? 'network' : 'local';
        if (nextWebAuthSettings.configured_exposure === 'network') webAuthListenAddr = nextWebAuthSettings.configured_listener;
      } else if (currentTab === 'logs' && selectedSessionID) {
        await fetchLogs();
        // If the URL carried ?q=, run that search once on first load so a shared
        // link / refreshed tab lands directly on the search results.
        if (!didAutoSearchFromURL && searchQuery) {
          didAutoSearchFromURL = true;
          await searchLogs();
        }
      } else if (currentTab === 'room_formats') {
        mapperProfiles = await api.fetchMapperProfiles() || [];
      } else if (currentTab === 'profiles') {
        profileFiles = await api.fetchProfileFiles() || [];
      } else if (currentTab === 'sessions' && selectedSessionID) {
        sessionProfiles = await api.fetchSessionProfiles(selectedSessionID) || [];
      } else if (currentTab === 'variables' && selectedSessionID) {
        variables = await api.fetchSessionVariables(selectedSessionID) || [];
      } else if (currentTab === 'history' && selectedSessionID) {
        await fetchHistory(false);
      } else if (currentTab === 'timers' && profiles.length > 0) {
        const timersMap: Record<number, ProfileTimer[]> = {};
        const nextTimerForms = { ...newTimerForms };
        const nextSubForms = { ...newSubForms };

        for (const p of profiles) {
          const list = await api.fetchProfileTimers(p.id) || [];
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
        const data = await api.fetchProfileDomain(selectedProfileID, currentTab) || [];
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

  // --- Room formats ("mapper profiles") ------------------------------------
  // The BACKEND is the single source of validation truth: every draft is
  // compile-checked by POST /api/mapper-profiles/validate and whatever message
  // comes back is shown verbatim. Nothing here reimplements a mapper.Compile
  // rule — that is how the two would silently diverge.

  // mapperValidateSeq sequences the validate requests. Without it a slow earlier
  // response can land after a faster later one and paint "Format is valid." over
  // a form the user has since changed or cleared — the UI claiming the backend
  // approved something it never saw.
  let mapperValidateSeq = 0;

  async function validateMapperProfileDraft() {
    const seq = ++mapperValidateSeq;
    mapperProfileError = '';
    mapperProfileValidated = false;
    clearMapperReplay();
    if (!mapperForm.name.trim()) return;
    try {
      const resp = await api.validateMapperProfile(mapperForm.name.trim(), formToSpec(mapperForm));
      if (seq !== mapperValidateSeq) return; // superseded — drop it
      if (!resp.ok) {
        // A non-2xx (401 after a token rotation, 500) has no JSON body to parse;
        // parsing it anyway surfaced a raw SyntaxError to the user.
        mapperProfileError = (await resp.text()) || `Validation failed (HTTP ${resp.status}).`;
        return;
      }
      const out = await resp.json();
      if (seq !== mapperValidateSeq) return;
      if (out.ok) {
        mapperProfileValidated = true;
      } else {
        mapperProfileError = out.error || 'Room format is not valid.';
      }
    } catch (e) {
      if (seq !== mapperValidateSeq) return;
      mapperProfileError = e instanceof Error ? e.message : String(e);
    }
  }

  async function replayMapperProfileDraft() {
    if (mapperReplaySessionID === null || mapperReplayState.busy) return;
    const request = mapperReplayRequest.begin(mapperForm, mapperReplaySessionID, mapperReplayLimit);
    mapperReplayState = mapperReplayRequest.state();
    try {
      const resp = await api.replayMapperProfile(
        request.name,
        request.spec,
        request.sessionID,
        request.limit,
      );
      if (!mapperReplayRequest.isCurrent(request, mapperForm, mapperReplaySessionID, mapperReplayLimit)) return;
      if (!resp.ok) {
        const replayError = (await resp.text()) || `Replay failed (HTTP ${resp.status}).`;
        if (!mapperReplayRequest.isCurrent(request, mapperForm, mapperReplaySessionID, mapperReplayLimit)) return;
        mapperReplayState = mapperReplayRequest.fail(request, mapperForm, mapperReplaySessionID, mapperReplayLimit, replayError);
        return;
      }
      const result = await resp.json();
      if (!mapperReplayRequest.isCurrent(request, mapperForm, mapperReplaySessionID, mapperReplayLimit)) return;
      mapperReplayState = mapperReplayRequest.succeed(request, mapperForm, mapperReplaySessionID, mapperReplayLimit, result);
    } catch (e) {
      if (!mapperReplayRequest.isCurrent(request, mapperForm, mapperReplaySessionID, mapperReplayLimit)) return;
      mapperReplayState = mapperReplayRequest.fail(request, mapperForm, mapperReplaySessionID, mapperReplayLimit, e instanceof Error ? e.message : String(e));
    } finally {
      mapperReplayState = mapperReplayRequest.finish(request);
    }
  }

  async function saveMapperProfile() {
    const name = mapperForm.name.trim();
    if (!name) {
      mapperProfileError = 'A format name is required.';
      return;
    }
    mapperProfileError = '';
    mapperProfileValidated = false;
    clearMapperReplay();
    // Any in-flight validate is now stale; its response must not repaint over the
    // save's result.
    mapperValidateSeq++;
    const spec = formToSpec(mapperForm);
    try {
      const resp = editingMapperProfileID === null
        ? await api.createMapperProfile(name, spec)
        : await api.saveMapperProfile(editingMapperProfileID, name, spec);
      if (!resp.ok) {
        // The body is mapper.Compile's message (or the name-conflict / version
        // -conflict message) — shown verbatim, the backend is the validation truth.
        mapperProfileError = (await resp.text()) || `Could not save the room format (HTTP ${resp.status}).`;
        return;
      }
      cancelMapperProfileEdit();
      await fetchData();
    } catch (e) {
      mapperProfileError = e instanceof Error ? e.message : String(e);
    }
  }

  // mapperFormDirty: is there an in-progress draft worth protecting? A named
  // form is the cheap proxy the other editors use (they compare ids); this
  // section has a single shared editor, so the name is the signal.
  $: mapperFormDirty = mapperForm.name.trim() !== '';

  function startMapperProfileEdit(profile: MapperProfile) {
    // A builtin is not a row — copy it instead. A contributed Go detector is not
    // even that: it has no spec, so there is nothing for the editor to open.
    if (profile.builtin || !profile.spec) return;
    // Matches the eight neighbouring editors: never destroy a draft silently.
    if (editingMapperProfileID !== profile.id && mapperFormDirty
        && !confirm('Discard unsaved room format changes?')) return;
    mapperProfileError = '';
    mapperProfileValidated = false;
    clearMapperReplay();
    editingMapperProfileID = profile.id;
    mapperForm = specToForm(profile.spec, profile.name);
  }

  // copyMapperProfile is the ONLY way to base a format on the builtin, which has
  // no row to edit: it pre-fills the create form from the source's spec.
  function copyMapperProfile(profile: MapperProfile) {
    if (mapperFormDirty && !confirm('Discard unsaved room format changes?')) return;
    mapperProfileError = '';
    mapperProfileValidated = false;
    clearMapperReplay();
    editingMapperProfileID = null;
    mapperForm = copyOfMapperProfile(profile);
  }

  function cancelMapperProfileEdit() {
    editingMapperProfileID = null;
    mapperForm = emptyMapperProfileForm();
    mapperProfileError = '';
    mapperProfileValidated = false;
    clearMapperReplay();
  }

  async function deleteMapperProfileRow(id: number) {
    if (!confirm('Delete this room format? Sessions using it will fall back to the builtin.')) return;
    try {
      const resp = await api.deleteMapperProfile(id);
      if (!resp.ok) {
        mapperProfileError = (await resp.text()) || `Could not delete the room format (HTTP ${resp.status}).`;
        return;
      }
      if (editingMapperProfileID === id) cancelMapperProfileEdit();
      await fetchData();
    } catch (e) {
      mapperProfileError = e instanceof Error ? e.message : String(e);
    }
  }

  function historyKindLabel(kind: string): string {
    return historyKindLabels[kind] || kind;
  }

  async function fetchHistory(append: boolean) {
    if (!selectedSessionID) return;
    if (append && !historyNextBeforeID) return;

    historyLoading = true;
    formError = '';
    try {
      const data = await api.fetchSessionHistory(selectedSessionID, {
        kind: historyKindFilter,
        query: historyQuery,
        beforeID: append ? historyNextBeforeID : null,
        limit: historyLimit,
      });
      const entries = data.entries || [];
      historyEntries = append ? [...historyEntries, ...entries] : entries;
      historyHasMore = !!data.has_more;
      historyNextBeforeID = data.next_before_id || null;
    } catch (e) {
      console.error('Failed to fetch history', e);
      formError = 'Failed to fetch history.';
    } finally {
      historyLoading = false;
    }
  }

  function applyHistorySearch() {
    historyQuery = historyQueryDraft.trim();
    void fetchHistory(false);
  }

  function clearHistorySearch() {
    historyQueryDraft = '';
    historyQuery = '';
    void fetchHistory(false);
  }

  async function saveItem(domain: string, item: any, resetFn: () => void) {
    const validationError = validateItem(domain, item);
    if (validationError) { formError = validationError; return; }

    formError = '';
    const resp = await api.saveItemRequest(domain, item, {
      selectedSessionID,
      selectedProfileID,
      variableKey: newVarKey,
      variableValue: newVarValue,
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
    const resp = await api.saveRuntimeVariable(selectedSessionID, editingVariableDraft.key, editingVariableDraft.value);
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
    const resp = await api.saveProfileRule(selectedProfileID, 'aliases', editingAliasDraft.id, editingAliasDraft);
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
    const resp = await api.saveProfileRule(selectedProfileID, 'triggers', editingTriggerDraft.id, editingTriggerDraft);
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
    const resp = await api.saveProfileRule(selectedProfileID, 'subs', editingSubDraft.id, editingSubDraft);
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
    const resp = await api.saveProfileRule(selectedProfileID, 'highlights', editingHighlightDraft.id, editingHighlightDraft);
    if (!resp.ok) {
      inlineHighlightError = await resp.text() || 'Failed to save highlight.';
      return;
    }
    cancelInlineHighlightEdit();
    await fetchData();
  }

  async function addHotkey() {
    const validationError = validateItem('hotkeys', hotkeyEditor) || duplicateHotkeyShortcutError(hotkeys, hotkeyEditor);
    if (validationError) { formError = validationError; return; }
    await saveItem('hotkeys', { ...hotkeyEditor, id: undefined, position: undefined }, () => hotkeyEditor = defaultHotkey());
  }

  function startInlineHotkeyEdit(hotkey: Hotkey) {
    if (hotkey.id === undefined) return;
    if (editingHotkeyID !== null && editingHotkeyID !== hotkey.id && !confirm('Discard unsaved hotkey changes?')) return;
    inlineHotkeyError = '';
    editingHotkeyID = hotkey.id;
    editingHotkeyDraft = { ...hotkey };
  }

  function cancelInlineHotkeyEdit() {
    inlineHotkeyError = '';
    editingHotkeyID = null;
    editingHotkeyDraft = defaultHotkey();
  }

  async function saveInlineHotkeyEdit() {
    if (!selectedProfileID || editingHotkeyDraft.id === undefined) return;
    const validationError = validateItem('hotkeys', editingHotkeyDraft) || duplicateHotkeyShortcutError(hotkeys, editingHotkeyDraft, editingHotkeyDraft.id);
    if (validationError) { inlineHotkeyError = validationError; return; }

    inlineHotkeyError = '';
    formError = '';
    const resp = await api.saveProfileRule(selectedProfileID, 'hotkeys', editingHotkeyDraft.id, editingHotkeyDraft);
    if (!resp.ok) {
      inlineHotkeyError = await resp.text() || 'Failed to save hotkey.';
      return;
    }
    cancelInlineHotkeyEdit();
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
    const resp = await api.saveProfileRule(selectedProfileID, 'declared_variables', editingProfileVariableDraft.id, editingProfileVariableDraft);
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
    const resp = await api.saveProfile(editingProfileDraft.id, editingProfileDraft);
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
    editingSessionDraft = { ...session, ansi_theme: session.ansi_theme || 'classic', timezone: session.timezone || 'UTC', tz_follow: session.tz_follow ?? 1 };
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
    const resp = await api.saveSession(editingSessionDraft.id, editingSessionDraft);
    if (!resp.ok) {
      inlineSessionError = await resp.text() || 'Failed to save session.';
      return;
    }
    cancelInlineSessionEdit();
    await fetchData();
  }

  async function deleteItem(domain: string, id: string | number) {
    if (!confirm(`Delete this ${domain.slice(0, -1)}?`)) return;
    const resp = await api.deleteItemRequest(domain, id, { selectedSessionID, selectedProfileID });
    if (!resp.ok) {
      formError = await resp.text() || `Failed to delete ${domain.slice(0, -1)}.`;
      return;
    }
    await fetchData();
  }

  async function toggleItem(domain: 'aliases' | 'triggers' | 'subs' | 'highlights', item: any, enabled: boolean) {
    if (!selectedProfileID) return;
    formError = '';
    await api.toggleProfileRule(selectedProfileID, domain, item, enabled);
    await fetchData();
  }

  async function toggleGroup(groupName: string, enabled: boolean) {
    if (!selectedProfileID) return;
    formError = '';
    await api.toggleProfileGroup(selectedProfileID, groupName, enabled);
    await fetchData();
  }

  async function saveProfileTimer(profileID: number, timer: any) {
    if (!timer.name || !timer.name.trim()) {
      alert("Ticker name is required");
      return;
    }
    const res = await api.saveProfileTimerRequest(profileID, timer);
    if (!res.ok) {
      alert("Failed to save ticker: " + await res.text());
    } else {
      await fetchData();
    }
  }

  async function deleteProfileTimer(profileID: number, name: string) {
    if (!confirm(`Delete ticker "${name}"? This will also delete all its subscriptions.`)) return;
    const res = await api.deleteProfileTimerRequest(profileID, name);
    if (!res.ok) {
      alert("Failed to delete ticker: " + await res.text());
    } else {
      await fetchData();
    }
  }

  async function saveProfileTimerSubscription(profileID: number, timerName: string, sub: any) {
    const res = await api.saveProfileTimerSubscriptionRequest(profileID, timerName, sub);
    if (!res.ok) {
      alert("Failed to save subscription: " + await res.text());
    } else {
      await fetchData();
    }
  }

  async function deleteProfileTimerSubscription(profileID: number, timerName: string, sub: any) {
    if (!confirm(`Delete subscription for second ${sub.second}?`)) return;
    const res = await api.deleteProfileTimerSubscriptionRequest(profileID, timerName, sub);
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
    await api.sessionActionRequest(action, id);
    await fetchData();
  }

  // syncLogsURL mirrors the current Logs filter into the URL query (?from&to&q)
  // via replaceState (no history spam), preserving the `#logs` tab hash. So a
  // refresh restores the same view and the URL can be shared / opened in a tab.
  function syncLogsURL() {
    if (currentTab !== 'logs') return;
    const p = new URLSearchParams(window.location.search);
    logFrom ? p.set('from', logFrom) : p.delete('from');
    logTo ? p.set('to', logTo) : p.delete('to');
    searchQuery ? p.set('q', searchQuery) : p.delete('q');
    const qs = p.toString();
    history.replaceState(null, '', window.location.pathname + (qs ? '?' + qs : '') + window.location.hash);
  }

  // onLogRangeChange: the datetime range drives the browse preview and the
  // exports; refetch the preview and persist the new range to the URL.
  function onLogRangeChange() {
    logPage = 1;
    searchMode = false;
    contextMode = false;
    syncLogsURL();
    void fetchLogs();
  }

  async function fetchLogs() {
    if (!selectedSessionID) return;
    loading = true;
    const from = localDateTimeISO(logFrom);
    const to = localDateTimeISO(logTo);

    try {
        const data = await api.fetchLogsRequest(selectedSessionID, from, to, logPage, logLimit);
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
    syncLogsURL();

    if (!loadMore) {
        searchResults = [];
        searchCursor = null;
        logEntries = []; // Clear browsing entries too to avoid confusion
    }

    try {
        const data = await api.searchLogsRequest(selectedSessionID, searchQuery, loadMore ? searchCursor : null) || {};
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
    try {
        contextEntries = await api.fetchLogContext(selectedSessionID, entryID) || [];
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

    try {
        const data: LogEntry[] = await api.fetchMoreLogContext(selectedSessionID, anchor.id, direction) || [];
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
    const from = localDateTimeISO(logFrom);
    const to = localDateTimeISO(logTo);
    window.open(api.buildLogDownloadURL(selectedSessionID, from, to), '_blank');
  }

  // Opens the server-side streaming colored-HTML export in a new tab, which
  // downloads a self-contained .html file. Server-side streaming (like the .txt
  // download) means no browser-side assembly, no size caps, and no OOM risk.
  function exportLogsHtml() {
    if (!selectedSessionID) return;
    const from = localDateTimeISO(logFrom);
    const to = localDateTimeISO(logTo);
    const theme = normalizeAnsiTheme(currentSession?.ansi_theme);
    const url = api.buildLogExportHTMLURL(selectedSessionID, {
      from,
      to,
      commands: exportIncludeCommands,
      theme,
      // Scope to the main game buffer: canonical command echoes anchored to side
      // buffers would otherwise leak into the export. main = the game screen.
      buffer: 'main',
    });
    window.open(url, '_blank');
  }

  async function addProfileToSession(profileID: number) {
    if (!selectedSessionID) return;
    const maxOrder = sessionProfiles.length > 0 ? Math.max(...sessionProfiles.map(sp => sp.order_index)) : -1;
    await api.addProfileToSessionRequest(selectedSessionID, profileID, maxOrder + 1);
    await fetchData();
  }

  async function removeProfileFromSession(profileID: number) {
    if (!selectedSessionID) return;
    await api.removeProfileFromSessionRequest(selectedSessionID, profileID);
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
    await api.reorderSessionProfilesRequest(selectedSessionID, payload);
    await fetchData();
  }

  async function exportProfile(profileID: number) {
    const res = await api.exportProfileRequest(profileID);
    if (!res.ok) { alert('Export failed: ' + await res.text()); return; }
    const data = await res.json();
    alert(`Exported to config/${data.filename}`);
    await fetchData();
  }

  async function exportAllProfiles() {
    const res = await api.exportAllProfilesRequest();
    if (!res.ok) { alert('Export failed: ' + await res.text()); return; }
    const filenames: string[] = await res.json();
    alert(`Exported ${filenames.length} profile(s) to config/`);
    await fetchData();
  }

  async function importAllProfiles() {
    if (!confirm('Import all .tt files from config/? This will create new profiles for each file.')) return;
    const res = await api.importAllProfilesRequest();
    if (!res.ok) { alert('Import failed: ' + await res.text()); return; }
    await fetchData();
  }

  async function importProfileFromFile(filename: string) {
    if (!confirm(`Import profile from ${filename}?`)) return;
    const res = await api.importProfileFromFileRequest(filename, selectedSessionID);
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

    // Optimistic update
    item.position = swapPos;
    swapWith.position = currentPos;

    await api.moveProfileRulesRequest(selectedProfileID, domain, item, swapWith);

    await fetchData();
  }

  async function rotateAPIToken() {
    if (!confirm('Are you sure you want to rotate the API token? This will invalidate all external clients.')) return;
    const res = await api.rotateAPITokenRequest();
    appSettings = await res.json();
    api.setAPIToken(appSettings.api_token);
  }

  async function saveAppCommandSetting(key: 'allow_exec_command' | 'allow_webfetch_command', enabled: boolean) {
    const next = { ...appSettings, [key]: enabled };
    const res = await api.saveAppSettingsRequest(next);
    if (!res.ok) {
      formError = await res.text() || 'Failed to save app settings.';
      return;
    }
    appSettings = await res.json();
    formError = '';
  }

  async function saveMCPEnabled(enabled: boolean) {
    if (mcpBusy) return;
    const revision = ++mcpRevision;
    mcpBusy = true;
    mcpCopyStatus = '';
    try {
      const res = await api.saveMCPSettingsRequest(enabled);
      if (!res.ok) {
        formError = await res.text() || 'Failed to update MCP settings.';
        await refreshMCPSettings(revision);
        return;
      }
      await acceptMCPWrite(await res.json() as MCPSettingsWriteResponse, revision);
      formError = '';
    } catch {
      formError = 'The update response was lost. Check the current status; rotate the credential if no token was received.';
      await refreshMCPSettings(revision);
    } finally {
      if (revision === mcpRevision) mcpBusy = false;
    }
  }

  async function saveWebAuthEnabled(enabled: boolean) {
    if (webAuthBusy || webAuthSettings.externally_managed) return;
    if (!enabled && !confirm('Disable web access protection?')) {
      webAuthSettings = { ...webAuthSettings };
      return;
    }
    webAuthBusy = true;
    try {
      const res = await api.saveWebAuthSettingsRequest({ enabled });
      if (!res.ok) {
        formError = await res.text() || 'Failed to update web access protection.';
        webAuthSettings = { ...webAuthSettings };
        return;
      }
      webAuthSettings = await res.json();
      webAuthUsername = webAuthSettings.username;
      formError = '';
      if (enabled) window.location.reload();
    } catch {
      formError = 'Web access protection may have changed. Reload the page to verify the current credentials.';
    } finally {
      webAuthBusy = false;
    }
  }

  async function saveWebAuthCredentials() {
    if (webAuthBusy || webAuthSettings.externally_managed) return;
    if (webAuthPassword !== webAuthPasswordConfirm) {
      formError = 'Passwords do not match.';
      return;
    }
    webAuthBusy = true;
    try {
      const res = await api.saveWebAuthSettingsRequest({
        enabled: true,
        username: webAuthUsername,
        password: webAuthPassword,
      });
      if (!res.ok) {
        formError = await res.text() || 'Failed to save web access credentials.';
        return;
      }
      webAuthSettings = await res.json();
      webAuthListenerMode = webAuthSettings.configured_exposure === 'network' ? 'network' : 'local';
      if (webAuthSettings.configured_exposure === 'network') webAuthListenAddr = webAuthSettings.configured_listener;
      webAuthPassword = '';
      webAuthPasswordConfirm = '';
      formError = '';
      window.location.reload();
    } catch {
      formError = 'Web access credentials may have changed. Reload the page and sign in with the new credentials.';
    } finally {
      webAuthBusy = false;
    }
  }

  async function saveWebAuthListener() {
    if (webAuthBusy) return;
    webAuthBusy = true;
    try {
      const listen_addr = webAuthListenerMode === 'local' ? '127.0.0.1:8080' : webAuthListenAddr;
      const res = await api.saveWebAuthSettingsRequest({
        enabled: webAuthSettings.enabled,
        listen_addr,
      });
      if (!res.ok) {
        formError = await res.text() || 'Failed to save web listener.';
        return;
      }
      webAuthSettings = await res.json();
      webAuthListenerMode = webAuthSettings.configured_exposure === 'network' ? 'network' : 'local';
      if (webAuthSettings.configured_exposure === 'network') webAuthListenAddr = webAuthSettings.configured_listener;
      formError = '';
    } catch {
      formError = 'Web listener settings may have changed. Reload the page to verify them.';
    } finally {
      webAuthBusy = false;
    }
  }

  async function rotateMCPToken() {
    if (mcpBusy) return;
    const revision = ++mcpRevision;
    mcpBusy = true;
    mcpCopyStatus = '';
    try {
      const res = await api.rotateMCPTokenRequest();
      if (!res.ok) {
        formError = await res.text() || 'Failed to rotate MCP credential.';
        await refreshMCPSettings(revision);
        return;
      }
      await acceptMCPWrite(await res.json() as MCPSettingsWriteResponse, revision);
      formError = '';
    } catch {
      formError = 'The rotation response was lost. The credential may have changed; rotate it again to receive a usable token.';
      await refreshMCPSettings(revision);
    } finally {
      if (revision === mcpRevision) mcpBusy = false;
    }
  }

  async function refreshMCPSettings(revision: number) {
    try {
      const current = await api.fetchMCPSettings();
      if (revision === mcpRevision) mcpSettings = current;
    } catch {
      // Keep the write error, which has the actionable recovery path.
    }
  }

  async function acceptMCPWrite(result: MCPSettingsWriteResponse, revision: number) {
    if (revision !== mcpRevision) return;
    const { token = '', ...settings } = result;
    mcpSettings = settings;
    mcpOneTimeToken = token;
    if (token) {
      await tick();
      mcpTokenInput?.focus();
      mcpTokenInput?.select();
    }
  }

  async function copyMCPToken() {
    const token = mcpOneTimeToken;
    try {
      await navigator.clipboard.writeText(token);
      if (token === mcpOneTimeToken) mcpCopyStatus = 'Copied.';
    } catch {
      if (token === mcpOneTimeToken) {
        mcpCopyStatus = 'Copy failed. Select the credential and copy it manually.';
        mcpTokenInput?.focus();
        mcpTokenInput?.select();
      }
    }
  }

  function closeMCPToken() {
    mcpOneTimeToken = '';
    mcpCopyStatus = '';
  }

  function openMCPDialog(node: HTMLDialogElement) {
    if (typeof node.showModal === 'function') node.showModal();
    else node.setAttribute('open', '');
    return {
      destroy() {
        if (node.open && typeof node.close === 'function') node.close();
      }
    };
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

    // Small polling refresh to keep connection status / MCCP stats live on the
    // sessions page. Suppressed while a row is being edited: reloading `sessions`
    // mid-edit steals focus and, worse, an in-flight poll response can land after
    // a save and overwrite the freshly-saved row with stale data — making an edit
    // (e.g. timezone) look like it didn't persist when it actually did.
    monitoringInterval = window.setInterval(() => {
      if (currentTab === 'sessions' && !loading && editingSessionID === null) {
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
        {editingHotkeyID}
        bind:editingHotkeyDraft
        {inlineHotkeyError}
        {addHotkey}
        moveHotkey={(hotkey, direction) => moveRule('hotkeys', hotkey, direction)}
        {startInlineHotkeyEdit}
        deleteHotkey={(id) => deleteItem('hotkeys', id)}
        {saveInlineHotkeyEdit}
        {cancelInlineHotkeyEdit}
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
        {toggleGroup}
      />

    {:else if currentTab === 'room_formats'}
      <MapperProfilesSection
        {mapperProfiles}
        {sessions}
        bind:mapperForm
        {editingMapperProfileID}
        {mapperProfileError}
        {mapperProfileValidated}
        bind:mapperReplaySessionID
        bind:mapperReplayLimit
        mapperReplayResult={mapperReplayState.result}
        mapperReplayError={mapperReplayState.error}
        mapperReplayBusy={mapperReplayState.busy}
        {replayMapperProfileDraft}
        replayMapperProfileChanged={clearMapperReplay}
        {saveMapperProfile}
        {validateMapperProfileDraft}
        {startMapperProfileEdit}
        {copyMapperProfile}
        {cancelMapperProfileEdit}
        {deleteMapperProfileRow}
      />

    {:else if currentTab === 'map_sets'}
      <MapSetsSection />

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
      <header class="content-header">
        <h2>History</h2>
        <p class="description">Stored command history rows for {currentSession?.name || 'selected session'}, newest first. Use Logs for canonical output/search/export.</p>
      </header>

      <div class="editor-box history-controls">
        <div class="form-row history-toolbar">
          <label class="history-filter" for="history-kind-filter">
            Kind
            <select id="history-kind-filter" bind:value={historyKindFilter} on:change={() => fetchHistory(false)} disabled={historyLoading}>
              {#each historyKindOptions as option}
                <option value={option.value}>{option.label}</option>
              {/each}
            </select>
          </label>
          <label class="history-filter history-search" for="history-search-query">
            Command search
            <input id="history-search-query" type="text" bind:value={historyQueryDraft} placeholder="Search command history..." on:keydown={(e) => e.key === 'Enter' && applyHistorySearch()} disabled={historyLoading} />
          </label>
          <button class="btn-secondary" on:click={applyHistorySearch} disabled={historyLoading}>{historyLoading ? 'Searching...' : 'Search'}</button>
          <button class="btn-secondary" on:click={clearHistorySearch} disabled={historyLoading || (!historyQuery && !historyQueryDraft)}>Clear</button>
          <button class="btn-secondary" on:click={() => fetchHistory(false)} disabled={historyLoading}>{historyLoading ? 'Refreshing...' : 'Refresh'}</button>
        </div>
        <p class="history-note">Search filters stored command history rows by command text only. Use Logs for canonical output/search/export.</p>
      </div>

      {#if formError}
        <div class="form-error">{formError}</div>
      {/if}

      <table class="data-table">
        <thead><tr><th>Time</th><th>Kind</th><th>Command</th><th style="width: 100px">Actions</th></tr></thead>
        <tbody>
          {#each historyEntries as entry}
            <tr>
              <td class="dim-cell" title={entry.created_at}>{new Date(entry.created_at).toLocaleString()}</td>
              <td class="dim-cell" title={entry.kind}>{historyKindLabel(entry.kind)}</td>
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
      {#if historyHasMore}
        <button class="load-more-btn" on:click={() => fetchHistory(true)} disabled={historyLoading}>{historyLoading ? 'Loading...' : 'Load older history'}</button>
      {/if}

    {:else if currentTab === 'logs'}
      <header class="content-header">
          <h2>Logs</h2>
          <p class="description">Browse and download historical logs for {currentSession?.name || 'selected session'}.</p>
      </header>

      <div class="editor-box">
          <div class="log-export">
              <!-- Row 1: date/time range — drives both the preview below and the exports. -->
              <div class="log-export__row">
                  <label class="log-export__field" for="log-from">
                      <span>From</span>
                      <input id="log-from" type="datetime-local" bind:value={logFrom} on:change={onLogRangeChange} />
                  </label>
                  <label class="log-export__field" for="log-to">
                      <span>To</span>
                      <input id="log-to" type="datetime-local" bind:value={logTo} on:change={onLogRangeChange} />
                  </label>
              </div>

              <!-- Row 2: export the selected range — two buttons, one option, one hint. -->
              <div class="log-export__row log-export__actions">
                  <button class="btn-secondary" on:click={downloadLogs} disabled={!selectedSessionID}>Download .txt</button>
                  <button class="btn-primary" on:click={exportLogsHtml} disabled={!selectedSessionID}>Download .html</button>
                  <label class="log-export__check">
                      <input type="checkbox" bind:checked={exportIncludeCommands} />
                      Include sent commands
                  </label>
              </div>
              <p class="log-export__note">
                  Exports the selected date/time range. <strong>.html</strong> is a self-contained colored file of the raw server output (no highlights or substitutions, nothing gagged) in this session's theme palette; <strong>.txt</strong> is plain text.
              </p>

              <!-- Row 3: search — independent of the export range (searches the whole session history). -->
              <div class="log-export__search">
                  <div class="form-row">
                      <input type="text" placeholder="Search logs..." bind:value={searchQuery} on:keydown={(e) => e.key === 'Enter' && searchLogs()} />
                      <button class="btn-primary" on:click={() => searchLogs()} disabled={!searchQuery || loading}>
                        {loading ? 'Searching...' : 'Search'}
                      </button>
                      {#if searchMode || contextMode}
                        <button class="btn-secondary" on:click={() => { searchMode = false; contextMode = false; searchQuery = ''; syncLogsURL(); fetchLogs(); }}>Back to Browsing</button>
                      {/if}
                  </div>
                  {#if loading && searchMode && searchResults.length === 0}
                    <div style="text-align: center; color: #3498db; margin-top: 0.75rem;">Searching logs for "{searchQuery}"...</div>
                  {/if}
                  <p class="log-export__note">
                      Search scans the whole session history and ignores the date range above — it does not affect what gets exported.
                  </p>
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
        {#if formError}<div class="form-error" role="alert">{formError}</div>{/if}
        <div class="editor-box">
            <h3>Wake Lock</h3>
            <p class="description">Keep the screen awake while the main client tab or installed app is visible. Local to this browser/PWA.</p>
            <label class="checkbox-label">
                <input type="checkbox" checked={wakeLockEnabled} on:change={(event) => setWakeLockEnabled((event.currentTarget as HTMLInputElement).checked)} />
                Keep screen awake while active
            </label>
        </div>
        <div class="editor-box">
            <h3>Local Command Hooks</h3>
            <p class="description">Power-user local commands are disabled by default. Enable only if you trust your profiles and triggers.</p>
            <label class="checkbox-label">
                <input type="checkbox" checked={appSettings.allow_exec_command} on:change={(event) => saveAppCommandSetting('allow_exec_command', (event.currentTarget as HTMLInputElement).checked)} />
                Allow #exec
            </label>
            <label class="checkbox-label">
                <input type="checkbox" checked={appSettings.allow_webfetch_command} on:change={(event) => saveAppCommandSetting('allow_webfetch_command', (event.currentTarget as HTMLInputElement).checked)} />
                Allow #webfetch
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
        <div class="editor-box integration-settings">
            <h3>Web Access</h3>
            <div class="integration-row">
                <label class="checkbox-label" title={webAuthSettings.externally_managed ? webAuthSettings.externally_managed_reason : ''}>
                    <input
                      type="checkbox"
                      checked={webAuthSettings.requested_enabled}
                      disabled={webAuthBusy || webAuthSettings.externally_managed || (!webAuthSettings.credential_configured && !webAuthSettings.requested_enabled)}
                      on:change={(event) => saveWebAuthEnabled((event.currentTarget as HTMLInputElement).checked)}
                    />
                    Protected
                </label>
                <span class:status-active={webAuthSettings.effective_enabled} class="integration-status">{webAuthSettings.effective_enabled ? 'Active' : 'Inactive'}</span>
                <span class:status-active={webAuthSettings.credential_configured} class="integration-status">{webAuthSettings.credential_configured ? 'Credentials configured' : 'No credentials'}</span>
            </div>
            {#if webAuthSettings.externally_managed_reason}
              <p class="integration-note">{webAuthSettings.externally_managed_reason}</p>
            {/if}
            <form class="credential-form" on:submit|preventDefault={saveWebAuthCredentials}>
                <label>
                    <span>Username</span>
                    <input bind:value={webAuthUsername} type="text" autocomplete="username" maxlength="128" disabled={webAuthBusy || webAuthSettings.externally_managed} required />
                </label>
                <label>
                    <span>New password</span>
                    <input bind:value={webAuthPassword} type="password" autocomplete="new-password" minlength="12" maxlength="72" disabled={webAuthBusy || webAuthSettings.externally_managed} required />
                </label>
                <label>
                    <span>Confirm password</span>
                    <input bind:value={webAuthPasswordConfirm} type="password" autocomplete="new-password" minlength="12" maxlength="72" disabled={webAuthBusy || webAuthSettings.externally_managed} required />
                </label>
                <div>
                    <button class="btn-secondary" type="submit" disabled={webAuthBusy || webAuthSettings.externally_managed || !webAuthUsername || webAuthPassword.length < 12 || webAuthPassword !== webAuthPasswordConfirm}>
                      {webAuthBusy ? 'Saving...' : webAuthSettings.effective_enabled ? 'Change credentials' : 'Save and enable'}
                    </button>
                </div>
            </form>
            <div class="listener-form">
                <div class="listener-form__header">
                    <span>Listener for next start</span>
                    {#if webAuthSettings.restart_required}<span class="integration-status status-active">Restart required</span>{/if}
                </div>
                <div class="listener-mode" role="group" aria-label="Web listener mode">
                    <button type="button" class:active={webAuthListenerMode === 'local'} disabled={webAuthBusy} on:click={() => webAuthListenerMode = 'local'}>Local only</button>
                    <button type="button" class:active={webAuthListenerMode === 'network'} disabled={webAuthBusy || !webAuthSettings.effective_enabled} on:click={() => webAuthListenerMode = 'network'}>Network access</button>
                </div>
                {#if webAuthListenerMode === 'network'}
                    <label class="listener-address">
                        <span>Listen address</span>
                        <input bind:value={webAuthListenAddr} type="text" inputmode="text" placeholder="0.0.0.0:8080" disabled={webAuthBusy} required />
                    </label>
                {/if}
                <div class="integration-row">
                    <span class="integration-status">Current: {webAuthSettings.current_listener} ({webAuthSettings.current_exposure})</span>
                    <span class="integration-status">Next: {webAuthSettings.configured_listener} ({webAuthSettings.configured_exposure})</span>
                    <button class="btn-secondary" type="button" disabled={webAuthBusy || (webAuthListenerMode === 'network' && !webAuthSettings.effective_enabled)} on:click={saveWebAuthListener}>{webAuthBusy ? 'Saving...' : 'Save listener'}</button>
                </div>
                {#if webAuthSettings.listener_externally_managed}
                  <p class="integration-note">The current listener is set by <code>--listen</code>. The saved listener applies after starting without that flag.</p>
                {/if}
                {#if webAuthListenerMode === 'network' || webAuthSettings.configured_exposure === 'network'}
                  <p class="listener-security-warning">Network access sends a reusable Basic Auth password. Use a TLS reverse proxy outside a trusted local network.</p>
                {/if}
            </div>
        </div>
        <div class="editor-box integration-settings">
            <h3>Integration: MCP</h3>
            <div class="integration-row">
                <label class="checkbox-label" title={mcpSettings.enabled_externally_managed ? mcpSettings.externally_managed_reason : ''}>
                    <input type="checkbox" checked={mcpSettings.requested_enabled} disabled={mcpBusy || mcpSettings.enabled_externally_managed} on:change={(event) => saveMCPEnabled((event.currentTarget as HTMLInputElement).checked)} />
                    Enabled
                </label>
                <span class:status-active={mcpSettings.effective_enabled} class="integration-status">{mcpSettings.effective_enabled ? 'Active' : 'Inactive'}</span>
                <span class="integration-status">{mcpSettings.listener_exposure}</span>
                <span class:status-active={mcpSettings.token_configured} class="integration-status">{mcpSettings.token_configured ? 'Credential configured' : 'No credential'}</span>
            </div>
            {#if mcpSettings.externally_managed_reason}
              <p class="integration-note">{mcpSettings.externally_managed_reason}</p>
            {/if}
            <div class="form-row">
                <button class="btn-secondary" disabled={mcpBusy || mcpSettings.token_externally_managed} title={mcpSettings.token_externally_managed ? mcpSettings.externally_managed_reason : ''} on:click={rotateMCPToken}>{mcpBusy ? 'Saving...' : mcpSettings.token_configured ? 'Rotate credential' : 'Generate credential'}</button>
            </div>
        </div>
        {#if mcpOneTimeToken}
          <dialog class="token-dialog" use:openMCPDialog aria-labelledby="mcp-token-title" on:cancel={(event) => { event.preventDefault(); closeMCPToken(); }}>
            <h3 id="mcp-token-title">MCP credential</h3>
            <input bind:this={mcpTokenInput} type="text" readonly value={mcpOneTimeToken} aria-label="MCP credential" />
            <p>This credential cannot be retrieved again. Closing this dialog removes it from the page.</p>
            <p class="copy-status" aria-live="polite">{mcpCopyStatus}</p>
            <div class="form-row">
              <button class="btn-primary" on:click={copyMCPToken}>Copy</button>
              <button class="btn-secondary" on:click={closeMCPToken}>Close</button>
            </div>
          </dialog>
        {/if}
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
  .form-row select { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 6px 12px; border-radius: 6px; }
  .form-error { color: #ff7b72; margin-bottom: 16px; }
  .history-controls { margin-bottom: 18px; }
  .history-toolbar { justify-content: space-between; flex-wrap: wrap; }
  .history-filter { display: flex; gap: 8px; align-items: center; color: #9ba3af; }
  .history-note { color: #9ba3af; font-size: 0.85rem; margin: 12px 0 0; }
  .log-export { max-width: 680px; display: flex; flex-direction: column; gap: 14px; }
  .log-export__row { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 12px; margin: 0; }
  .log-export__field { display: flex; flex-direction: column; gap: 4px; }
  .log-export__field > span { font-size: 0.75rem; color: #9ba3af; }
  .log-export__field input { flex: none; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 6px 10px; border-radius: 6px; color-scheme: dark; font: inherit; }
  .log-export__field input[type="datetime-local"] { width: 210px; }
  .log-export__check { display: flex; align-items: center; gap: 6px; font-size: 0.85rem; color: #cbd5e1; cursor: pointer; white-space: nowrap; }
  .log-export__actions { align-items: center; }
  .log-export__note { color: #9ba3af; font-size: 0.8rem; margin: 2px 0 0; line-height: 1.4; }
  .log-export__search { margin-top: 16px; padding-top: 16px; border-top: 1px solid #2d333b; }
  .log-export__search .form-row input[type="text"] { max-width: 320px; }
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
  .integration-settings { display: flex; flex-direction: column; gap: 12px; }
  .integration-settings h3 { margin: 0; }
  .integration-row { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
  .integration-status { color: #9ba3af; font-size: 0.82rem; border-left: 1px solid #30363d; padding-left: 10px; }
  .status-active { color: #56d364; }
  .integration-note { color: #9ba3af; font-size: 0.85rem; margin: 0; }
  .credential-form { display: grid; grid-template-columns: repeat(3, minmax(160px, 260px)); gap: 12px; align-items: end; }
  .credential-form label { display: flex; flex-direction: column; gap: 5px; color: #9ba3af; font-size: 0.8rem; }
  .credential-form input { box-sizing: border-box; width: 100%; min-width: 0; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; font: inherit; }
  .listener-form { display: grid; gap: 9px; margin-top: 16px; padding-top: 14px; border-top: 1px solid #30363d; }
  .listener-form__header { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; color: #cbd5e1; font-size: 0.88rem; }
  .listener-mode { display: inline-flex; width: fit-content; border: 1px solid #30363d; border-radius: 6px; overflow: hidden; }
  .listener-mode button { border: 0; border-radius: 0; background: #161b22; color: #9ba3af; padding: 7px 10px; font: inherit; }
  .listener-mode button + button { border-left: 1px solid #30363d; }
  .listener-mode button.active { background: #1f6feb; color: #fff; }
  .listener-address { display: flex; flex-direction: column; gap: 5px; max-width: 320px; color: #9ba3af; font-size: 0.8rem; }
  .listener-address input { box-sizing: border-box; width: 100%; min-width: 0; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; font: inherit; }
  .listener-security-warning { margin: 0; color: #f0b429; font-size: 0.85rem; }
  .token-dialog { box-sizing: border-box; width: min(520px, calc(100vw - 36px)); max-height: calc(100vh - 36px); overflow: auto; background: #1a1d21; border: 1px solid #30363d; border-radius: 8px; padding: 20px; }
  .token-dialog::backdrop { background: rgba(0, 0, 0, 0.64); }
  .token-dialog h3 { margin: 0 0 14px; }
  .token-dialog input { width: 100%; box-sizing: border-box; font-family: monospace; }
  .token-dialog p { color: #9ba3af; font-size: 0.85rem; }
  .token-dialog .copy-status { min-height: 1.2em; color: #e8edf2; }

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
    .credential-form { grid-template-columns: 1fr; }
  }

</style>
