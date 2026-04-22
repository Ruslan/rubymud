<script lang="ts">
  import { onMount } from 'svelte';

  interface Variable {
    key: string;
    value: string;
  }

  interface Alias {
    id?: number;
    name: string;
    template: string;
    enabled: boolean;
    group_name: string;
  }

  interface RuleGroupSummary {
    domain: string;
    group_name: string;
    total_count: number;
    enabled_count: number;
    disabled_count: number;
  }

  interface Trigger {
    id?: number;
    name: string;
    pattern: string;
    command: string;
    enabled: boolean;
    is_button: boolean;
    stop_after_match: boolean;
    group_name: string;
  }

  interface Highlight {
    id?: number;
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

  // Initialize from hash or default to variables
  let currentTab = window.location.hash.slice(1) || 'variables';
  let loading = true;
  let formError = '';

  window.onhashchange = () => {
    currentTab = window.location.hash.slice(1) || 'variables';
  };

  $: if (currentTab) {
    if (window.location.hash !== '#' + currentTab) {
      window.location.hash = currentTab;
    }
  }

  const tabs = [
    { id: 'variables', label: 'Variables' },
    { id: 'aliases', label: 'Aliases' },
    { id: 'triggers', label: 'Triggers' },
    { id: 'highlights', label: 'Highlights' },
    { id: 'groups', label: 'Groups' },
    { id: 'sessions', label: 'Sessions' },
  ];

  // Variables State
  let variables: Variable[] = [];
  let newVarKey = '';
  let newVarValue = '';

  // Aliases State
  let aliases: Alias[] = [];
  const defaultAlias = (): Alias => ({ name: '', template: '', enabled: true, group_name: '' });
  let aliasEditor: Alias = defaultAlias();

  // Triggers State
  let triggers: Trigger[] = [];
  const defaultTrigger = (): Trigger => ({ name: '', pattern: '', command: '', enabled: true, is_button: false, stop_after_match: false, group_name: '' });
  let triggerEditor: Trigger = defaultTrigger();

  // Highlights State
  let highlights: Highlight[] = [];
  const defaultHighlight = (): Highlight => ({ pattern: '', fg: '', bg: '', bold: false, faint: false, italic: false, underline: false, strikethrough: false, blink: false, reverse: false, enabled: true, group_name: '' });
  let highlightEditor: Highlight = defaultHighlight();

  // Groups State
  let groups: RuleGroupSummary[] = [];

  type RGB = { r: number; g: number; b: number };

  const ansiBasePalette: RGB[] = [
    { r: 0, g: 0, b: 0 },
    { r: 128, g: 0, b: 0 },
    { r: 0, g: 128, b: 0 },
    { r: 128, g: 128, b: 0 },
    { r: 0, g: 0, b: 128 },
    { r: 128, g: 0, b: 128 },
    { r: 0, g: 128, b: 128 },
    { r: 192, g: 192, b: 192 },
    { r: 128, g: 128, b: 128 },
    { r: 255, g: 0, b: 0 },
    { r: 0, g: 255, b: 0 },
    { r: 255, g: 255, b: 0 },
    { r: 0, g: 0, b: 255 },
    { r: 255, g: 0, b: 255 },
    { r: 0, g: 255, b: 255 },
    { r: 255, g: 255, b: 255 },
  ];

  function ansi256ToRGB(index: number): RGB {
    if (index >= 0 && index < 16) return ansiBasePalette[index] ?? ansiBasePalette[0]!;
    if (index >= 16 && index <= 231) {
      const cube = index - 16;
      const steps = [0, 95, 135, 175, 215, 255];
      const r = steps[Math.floor(cube / 36) % 6] ?? 0;
      const g = steps[Math.floor(cube / 6) % 6] ?? 0;
      const b = steps[cube % 6] ?? 0;
      return {
        r,
        g,
        b,
      };
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
    if (hex.length === 3) {
      hex = `${hex[0]}${hex[0]}${hex[1]}${hex[1]}${hex[2]}${hex[2]}`;
    }
    if (!/^[0-9a-f]{6}$/.test(hex)) return null;
    return {
      r: parseInt(hex.slice(0, 2), 16),
      g: parseInt(hex.slice(2, 4), 16),
      b: parseInt(hex.slice(4, 6), 16),
    };
  }

  function colorValueToHex(value: string): string {
    const normalized = value.trim().toLowerCase();
    if (!normalized) return '#cccccc';
    const parsedHex = parseHexColor(normalized);
    if (parsedHex) return rgbToHex(parsedHex);
    if (normalized.startsWith('256:')) {
      const index = Number.parseInt(normalized.slice(4), 10);
      if (!Number.isNaN(index) && index >= 0 && index <= 255) {
        return rgbToHex(ansi256ToRGB(index));
      }
    }
    return '#cccccc';
  }

  function colorValueToCss(value: string, fallback: string): string {
    const normalized = value.trim().toLowerCase();
    if (!normalized) return fallback;
    if (parseHexColor(normalized)) return rgbToHex(parseHexColor(normalized)!);
    if (normalized.startsWith('256:')) {
      const index = Number.parseInt(normalized.slice(4), 10);
      if (!Number.isNaN(index) && index >= 0 && index <= 255) {
        return rgbToHex(ansi256ToRGB(index));
      }
    }
    return fallback;
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
      if (distance < bestDistance) {
        bestDistance = distance;
        bestIndex = i;
      }
    }
    return bestIndex;
  }

  function setHighlightColor(field: 'fg' | 'bg', hex: string) {
    const rgb = parseHexColor(hex);
    if (!rgb) return;
    highlightEditor = {
      ...highlightEditor,
      [field]: `256:${nearestAnsi256Index(rgb)}`,
    };
  }

  // Sessions State
  let sessions: any[] = [];

  async function fetchData() {
    loading = true;
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    try {
      if (currentTab === 'groups') {
        const [groupsRes, aliasesRes, triggersRes, highlightsRes] = await Promise.all([
          fetch('/api/groups', { headers: { 'X-Session-Token': token } }),
          fetch('/api/aliases', { headers: { 'X-Session-Token': token } }),
          fetch('/api/triggers', { headers: { 'X-Session-Token': token } }),
          fetch('/api/highlights', { headers: { 'X-Session-Token': token } }),
        ]);
        groups = await groupsRes.json() || [];
        aliases = await aliasesRes.json() || [];
        triggers = await triggersRes.json() || [];
        highlights = await highlightsRes.json() || [];
      } else {
        const res = await fetch(`/api/${currentTab}`, {
          headers: { 'X-Session-Token': token }
        });
        const data = await res.json();
        if (currentTab === 'variables') variables = data || [];
        else if (currentTab === 'aliases') aliases = data || [];
        else if (currentTab === 'triggers') triggers = data || [];
        else if (currentTab === 'highlights') highlights = data || [];
        else if (currentTab === 'sessions') sessions = data || [];
      }
    } catch (e) {
      console.error(`Failed to fetch ${currentTab}`, e);
    } finally {
      loading = false;
    }
  }

  async function saveItem(domain: string, item: any, resetFn: () => void) {
    const validationError = validateItem(domain, item);
    if (validationError) {
      formError = validationError;
      return;
    }

    formError = '';
    const isUpdate = !!item.id || domain === 'variables';
    let url = `/api/${domain}`;
    if (isUpdate && domain !== 'variables') url += `/${item.id}`;
    
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(url, {
      method: (isUpdate && domain !== 'variables') ? 'PUT' : 'POST',
      headers: { 
        'Content-Type': 'application/json',
        'X-Session-Token': token
      },
      body: JSON.stringify(domain === 'variables' ? { key: newVarKey, value: newVarValue } : item)
    });
    resetFn();
    await fetchData();
  }

  function validateItem(domain: string, item: any): string {
    if (domain === 'variables') {
      if (!newVarKey.trim()) return 'Variable key is required.';
      return '';
    }
    if (domain === 'aliases') {
      if (!item.name?.trim()) return 'Alias name is required.';
      if (!item.template?.trim()) return 'Alias template is required.';
      return '';
    }
    if (domain === 'triggers') {
      if (!item.pattern?.trim()) return 'Trigger pattern is required.';
      if (!item.command?.trim()) return 'Trigger command is required.';
      return '';
    }
    if (domain === 'highlights') {
      if (!item.pattern?.trim()) return 'Highlight pattern is required.';
      return '';
    }
    return '';
  }

  async function deleteItem(domain: string, id: string | number) {
    if (!confirm(`Delete this ${domain.slice(0, -1)}?`)) return;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    const itemID = domain === 'variables' ? encodeURIComponent(String(id)) : String(id);
    await fetch(`/api/${domain}/${itemID}`, { 
      method: 'DELETE',
      headers: { 'X-Session-Token': token }
    });
    await fetchData();
  }

  async function toggleItem(domain: 'aliases' | 'triggers' | 'highlights', item: Alias | Trigger | Highlight, enabled: boolean) {
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch(`/api/${domain}/${item.id}`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-Token': token,
      },
      body: JSON.stringify({ ...item, enabled }),
    });
    await fetchData();
  }

  async function toggleGroup(domain: string, groupName: string, enabled: boolean) {
    formError = '';
    // @ts-ignore
    const token = window.API_TOKEN || '';
    await fetch('/api/groups/toggle', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-Token': token,
      },
      body: JSON.stringify({ domain, group_name: groupName, enabled }),
    });
    await fetchData();
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

  $: if (currentTab) fetchData();
  onMount(() => fetchData());
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
    {#if currentTab === 'variables'}
      <header class="content-header"><h2>Variables</h2><p class="description">Global state variables.</p></header>
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

    {:else if currentTab === 'aliases'}
      <header class="content-header"><h2>Aliases</h2><p class="description">Command shortcuts.</p></header>
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
        <thead><tr><th style="width: 72px">Status</th><th>Name</th><th>Template</th><th>Group</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each aliases as a}
            <tr>
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
      <header class="content-header"><h2>Triggers</h2><p class="description">Automatic reactions.</p></header>
      <div class="editor-box">
        {#if formError}<div class="form-error">{formError}</div>{/if}
        <div class="form-grid">
          <div class="form-row"><input type="text" bind:value={triggerEditor.name} placeholder="Trigger Name" /><input type="text" bind:value={triggerEditor.pattern} placeholder="Pattern (Regex)" required /></div>
          <div class="form-row"><input type="text" bind:value={triggerEditor.command} placeholder="Command" required /><input type="text" bind:value={triggerEditor.group_name} placeholder="Group Name" /></div>
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
        <thead><tr><th style="width: 96px">Flags</th><th>Pattern</th><th>Command</th><th>Group</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each triggers as t}
            <tr>
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
              <td class="value-cell">{t.command}</td><td class="dim-cell">{t.group_name || '-'}</td>
              <td class="actions-cell">
                <button class="btn-link" on:click={() => triggerEditor = { ...t }}>Edit</button>
                <button class="btn-link btn-danger" on:click={() => deleteItem('triggers', t.id!)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>

    {:else if currentTab === 'highlights'}
      <header class="content-header"><h2>Highlights</h2><p class="description">Visual formatting.</p></header>
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
        <thead><tr><th style="width: 72px">Status</th><th>Pattern</th><th>Colors</th><th>Group</th><th>Preview</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each highlights as h}
            <tr>
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
    {:else if currentTab === 'groups'}
      <header class="content-header"><h2>Groups</h2><p class="description">Bulk enable or disable rule groups across aliases, triggers, and highlights.</p></header>
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
    {:else if currentTab === 'sessions'}
      <header class="content-header"><h2>Sessions</h2><p class="description">Read-only runtime session status for 0.0.5. Real connect/disconnect management moves to 0.0.6.</p></header>
      <table class="data-table">
        <thead><tr><th>Status</th><th>Name</th><th>Host</th><th>Port</th><th>Session ID</th></tr></thead>
        <tbody>
          {#each sessions as s}
            <tr>
              <td>
                <span class="status-tag {s.status === 'connected' ? 'connected' : 'disconnected'}">
                  {s.status}
                </span>
              </td>
              <td class="key-cell">{s.name}</td>
              <td class="value-cell">{s.mud_host || '-'}</td>
              <td class="dim-cell">{s.mud_port || '-'}</td>
              <td class="dim-cell">{s.id}</td>
            </tr>
          {/each}
        </tbody>
      </table>
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
  .form-grid { display: flex; flex-direction: column; gap: 12px; }
  .form-row { display: flex; gap: 12px; align-items: center; }
  .options-row { margin-top: 8px; border-top: 1px solid #2d333b; padding-top: 12px; flex-wrap: wrap; }
  input[type="text"] { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; flex: 1; }
  .checkbox-label { display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: #9ba3af; cursor: pointer; }
  .color-field { display: flex; align-items: center; gap: 10px; color: #9ba3af; min-width: 0; }
  .color-field span { font-size: 0.85rem; min-width: 20px; }
  .color-field code { color: #e8edf2; background: #0d1117; border: 1px solid #30363d; border-radius: 6px; padding: 6px 8px; min-width: 72px; }
  input[type="color"] { width: 44px; height: 32px; background: none; border: none; padding: 0; }
  .btn-primary { background: #3498db; color: white; border: none; padding: 8px 20px; border-radius: 6px; cursor: pointer; }
  .data-table { width: 100%; border-collapse: collapse; }
  .data-table th { text-align: left; padding: 12px; border-bottom: 2px solid #30363d; color: #9ba3af; font-size: 0.8rem; text-transform: uppercase; }
  .data-table td { padding: 12px; border-bottom: 1px solid #21262d; font-size: 0.9rem; vertical-align: middle; }
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
