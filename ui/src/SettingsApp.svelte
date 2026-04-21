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
    { id: 'sessions', label: 'Sessions' },
  ];

  // Variables State
  let variables: Variable[] = [];
  let newVarKey = '';
  let newVarValue = '';

  // Aliases State
  let aliases: Alias[] = [];
  let aliasEditor: Alias = { name: '', template: '', enabled: true };

  // Triggers State
  let triggers: Trigger[] = [];
  const defaultTrigger = (): Trigger => ({ name: '', pattern: '', command: '', enabled: true, is_button: false, stop_after_match: false, group_name: '' });
  let triggerEditor: Trigger = defaultTrigger();

  // Highlights State
  let highlights: Highlight[] = [];
  const defaultHighlight = (): Highlight => ({ pattern: '', fg: '', bg: '', bold: false, faint: false, italic: false, underline: false, strikethrough: false, blink: false, reverse: false, enabled: true, group_name: '' });
  let highlightEditor: Highlight = defaultHighlight();

  // Sessions State
  let sessions: any[] = [];

  async function fetchData() {
    loading = true;
    // @ts-ignore
    const token = window.API_TOKEN || '';
    try {
      const res = await fetch(`/api/${currentTab}`, {
        headers: { 'X-Session-Token': token }
      });
      const data = await res.json();
      if (currentTab === 'variables') variables = data || [];
      else if (currentTab === 'aliases') aliases = data || [];
      else if (currentTab === 'triggers') triggers = data || [];
      else if (currentTab === 'highlights') highlights = data || [];
      else if (currentTab === 'sessions') sessions = data || [];
    } catch (e) {
      console.error(`Failed to fetch ${currentTab}`, e);
    } finally {
      loading = false;
    }
  }

  async function saveItem(domain: string, item: any, resetFn: () => void) {
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
        <div class="form-row">
          <input type="text" bind:value={newVarKey} placeholder="Key" />
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
        <div class="form-row">
          <input type="text" bind:value={aliasEditor.name} placeholder="Name" />
          <input type="text" bind:value={aliasEditor.template} placeholder="Template" />
          <label class="checkbox-label"><input type="checkbox" bind:checked={aliasEditor.enabled} /> Enabled</label>
          <button class="btn-primary" on:click={() => saveItem('aliases', aliasEditor, () => aliasEditor = { name: '', template: '', enabled: true })}>
            {aliasEditor.id ? 'Update' : 'Add'}
          </button>
        </div>
      </div>
      <table class="data-table">
        <thead><tr><th style="width: 40px">Status</th><th>Name</th><th>Template</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each aliases as a}
            <tr>
              <td><span class="status-dot {a.enabled ? 'on' : 'off'}" title={a.enabled ? 'Enabled' : 'Disabled'}></span></td>
              <td class="key-cell">{a.name}</td><td class="value-cell">{a.template}</td>
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
        <div class="form-grid">
          <div class="form-row"><input type="text" bind:value={triggerEditor.name} placeholder="Trigger Name" /><input type="text" bind:value={triggerEditor.pattern} placeholder="Pattern (Regex)" /></div>
          <div class="form-row"><input type="text" bind:value={triggerEditor.command} placeholder="Command" /><input type="text" bind:value={triggerEditor.group_name} placeholder="Group Name" /></div>
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
        <thead><tr><th style="width: 80px">Flags</th><th>Pattern</th><th>Command</th><th>Group</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each triggers as t}
            <tr>
              <td class="flags-cell">
                <div class="flags-wrapper">
                  <span class={t.enabled ? 'flag-on' : 'flag-off'} title="Enabled">●</span>
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
        <div class="form-grid">
          <div class="form-row">
            <input type="text" bind:value={highlightEditor.pattern} placeholder="Pattern (Regex)" />
            <input type="text" bind:value={highlightEditor.group_name} placeholder="Group Name" />
          </div>
          <div class="form-row">
            <input type="text" bind:value={highlightEditor.fg} placeholder="FG (e.g. #ff0000)" />
            <input type="text" bind:value={highlightEditor.bg} placeholder="BG (e.g. #000033)" />
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
        <thead><tr><th style="width: 40px">Status</th><th>Pattern</th><th>Colors</th><th>Group</th><th>Preview</th><th style="width: 140px">Actions</th></tr></thead>
        <tbody>
          {#each highlights as h}
            <tr>
              <td><span class="status-dot {h.enabled ? 'on' : 'off'}" title={h.enabled ? 'Enabled' : 'Disabled'}></span></td>
              <td class="key-cell">{h.pattern}</td>
              <td class="dim-cell">
                <div class="color-preview">
                  <span class="color-chip" style="background: {h.fg || '#ccc'}"></span> {h.fg || 'def'}
                  <span class="color-chip" style="background: {h.bg || '#333'}"></span> {h.bg || 'def'}
                </div>
              </td>
              <td class="dim-cell">{h.group_name || '-'}</td>
              <td class="value-cell">
                <span style="
                  color: {h.reverse ? (h.bg || '#111') : (h.fg || '#eee')};
                  background: {h.reverse ? (h.fg || '#eee') : (h.bg || 'transparent')};
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
  .editor-box { background: #1a1d21; padding: 20px; border-radius: 8px; border: 1px solid #2d333b; margin-bottom: 32px; }
  .form-grid { display: flex; flex-direction: column; gap: 12px; }
  .form-row { display: flex; gap: 12px; align-items: center; }
  .options-row { margin-top: 8px; border-top: 1px solid #2d333b; padding-top: 12px; flex-wrap: wrap; }
  input[type="text"] { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; flex: 1; }
  .checkbox-label { display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: #9ba3af; cursor: pointer; }
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
  .btn-link { background: none; border: none; color: #3498db; cursor: pointer; padding: 0; font-size: 0.9rem; }
  .btn-link:hover { text-decoration: underline; }
  .btn-danger { color: #e74c3c; margin-left: 12px; }
</style>
