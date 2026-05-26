<script lang="ts">
  import './settings-section.css';
  import type { Highlight, NamedColor, Profile } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let highlights: Highlight[] = [];
  export let highlightEditor: Highlight;
  export let editingHighlightID: number | null = null;
  export let editingHighlightDraft: Highlight;
  export let inlineHighlightError = '';
  export let namedColorList: NamedColor[] = [];

  export let namedColorSelectValue: (value: string) => string;
  export let colorValueToHex: (value: string) => string;
  export let colorValueToCss: (value: string, fallback: string) => string;
  export let setHighlightColor: (field: 'fg' | 'bg', hex: string) => void;
  export let addHighlight: () => void | Promise<void>;
  export let moveHighlight: (highlight: Highlight, direction: -1 | 1) => void | Promise<void>;
  export let toggleHighlight: (highlight: Highlight, enabled: boolean) => void | Promise<void>;
  export let startInlineHighlightEdit: (highlight: Highlight) => void;
  export let deleteHighlight: (id: number) => void | Promise<void>;
  export let saveInlineHighlightEdit: () => void | Promise<void>;
  export let cancelInlineHighlightEdit: () => void;
</script>

<section class="settings-section">
<header class="content-header"><h2>Highlights</h2><p class="description">Visual formatting for profile {currentProfile?.name}.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-grid">
    <div class="form-row">
      <input type="text" bind:value={highlightEditor.pattern} placeholder="Pattern (Regex)" required />
      <input type="text" bind:value={highlightEditor.group_name} placeholder="Group Name" />
    </div>
    <div class="form-row color-controls">
      <label class="color-field">
        <span>FG</span>
        <select
          value={namedColorSelectValue(highlightEditor.fg)}
          on:change={(e) => {
            const v = (e.currentTarget as HTMLSelectElement).value;
            if (v !== '__custom__') highlightEditor = { ...highlightEditor, fg: v };
          }}
        >
          <option value="">default</option>
          {#each namedColorList as c}
            <option value={c.name}>{c.name}</option>
          {/each}
          {#if namedColorSelectValue(highlightEditor.fg) === '__custom__'}
            <option value="__custom__">custom</option>
          {/if}
        </select>
        <input type="color" value={colorValueToHex(highlightEditor.fg)} on:input={(event) => setHighlightColor('fg', (event.currentTarget as HTMLInputElement).value)} />
        <button type="button" class="btn-icon reset-btn" aria-label="Reset foreground color" on:click={() => highlightEditor = { ...highlightEditor, fg: '' }}>✕</button>
        <code>{highlightEditor.fg || 'default'}</code>
      </label>
      <label class="color-field">
        <span>BG</span>
        <select
          value={namedColorSelectValue(highlightEditor.bg)}
          on:change={(e) => {
            const v = (e.currentTarget as HTMLSelectElement).value;
            if (v !== '__custom__') highlightEditor = { ...highlightEditor, bg: v };
          }}
        >
          <option value="">default</option>
          {#each namedColorList as c}
            <option value={c.name}>{c.name}</option>
          {/each}
          {#if namedColorSelectValue(highlightEditor.bg) === '__custom__'}
            <option value="__custom__">custom</option>
          {/if}
        </select>
        <input type="color" value={colorValueToHex(highlightEditor.bg)} on:input={(event) => setHighlightColor('bg', (event.currentTarget as HTMLInputElement).value)} />
        <button type="button" class="btn-icon reset-btn" aria-label="Reset background color" on:click={() => highlightEditor = { ...highlightEditor, bg: '' }}>✕</button>
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
      <button class="btn-primary" on:click={addHighlight}>
        Add
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
           <button class="btn-icon" disabled={editingHighlightID === h.id || i === 0} on:click={() => moveHighlight(h, -1)}>▲</button>
           <button class="btn-icon" disabled={editingHighlightID === h.id || i === highlights.length - 1} on:click={() => moveHighlight(h, 1)}>▼</button>
        </td>
        <td>
          <label class="toggle-label">
            <input type="checkbox" checked={h.enabled} disabled={editingHighlightID === h.id} on:change={(event) => toggleHighlight(h, (event.currentTarget as HTMLInputElement).checked)} />
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
          {#if editingHighlightID === h.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineHighlightEdit(h)}>Edit</button>
            <button class="btn-link btn-danger" on:click={() => deleteHighlight(h.id!)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingHighlightID === h.id}
        <tr class="inline-edit-row">
          <td colspan="7">
            <div class="inline-edit-panel">
              <div class="inline-edit-grid">
                <label>Position <input class="inline-position-input" type="number" bind:value={editingHighlightDraft.position} aria-label="Highlight position" /></label>
                <label class="inline-field-wide">Pattern <input type="text" bind:value={editingHighlightDraft.pattern} aria-label="Highlight pattern" /></label>
                <label>Foreground <input type="text" bind:value={editingHighlightDraft.fg} aria-label="Highlight foreground" placeholder="FG" /></label>
                <label>Background <input type="text" bind:value={editingHighlightDraft.bg} aria-label="Highlight background" placeholder="BG" /></label>
                <label>Group <input type="text" bind:value={editingHighlightDraft.group_name} aria-label="Highlight group" /></label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.enabled} /> Enabled</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.bold} /> Bold</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.faint} /> Faint</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.italic} /> Italic</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.underline} /> Underline</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.strikethrough} /> Strike</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.blink} /> Blink</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingHighlightDraft.reverse} /> Reverse</label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineHighlightEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineHighlightEdit}>Cancel</button>
              </div>
              {#if inlineHighlightError}<div class="form-error inline-error">{inlineHighlightError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>

<style>
  @keyframes -global-blink { 0% { opacity: 1; } 50% { opacity: 0.2; } 100% { opacity: 1; } }
  .color-field { display: flex; align-items: center; gap: 8px; color: #9ba3af; min-width: 0; }
  .color-field span { font-size: 0.85rem; min-width: 20px; }
  .color-field select { background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 4px 6px; border-radius: 6px; font-size: 0.85rem; }
  .color-field code { color: #e8edf2; background: #0d1117; border: 1px solid #30363d; border-radius: 6px; padding: 6px 8px; min-width: 72px; font-size: 0.85rem; }
  .reset-btn { color: #ff7b72; padding: 4px; border-radius: 50%; width: 24px; height: 24px; display: flex; align-items: center; justify-content: center; font-size: 14px; }
  .reset-btn:hover { background: rgba(248, 81, 73, 0.15); color: #f85149; }
  .color-controls { flex-wrap: wrap; }
  input[type="color"] { width: 36px; height: 32px; background: none; border: none; padding: 0; cursor: pointer; }
  .status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; vertical-align: middle; }
  .status-dot.on { background: #2ecc71; box-shadow: 0 0 5px #2ecc71; }
  .status-dot.off { background: #95a5a6; }
  .color-preview { display: flex; align-items: center; gap: 4px; }
  .color-chip { width: 12px; height: 12px; border-radius: 2px; border: 1px solid #444; }
</style>
