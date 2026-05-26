<script lang="ts">
  import './settings-section.css';
  import type { Profile, Substitute } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let subs: Substitute[] = [];
  export let subEditor: Substitute;
  export let subPreviewText = 'foo bar';
  export let editingSubID: number | null = null;
  export let editingSubDraft: Substitute;
  export let inlineSubError = '';

  export let previewSubstitution: (sub: Substitute) => string;
  export let addSubstitution: () => void | Promise<void>;
  export let moveSubstitution: (sub: Substitute, direction: -1 | 1) => void | Promise<void>;
  export let toggleSubstitution: (sub: Substitute, enabled: boolean) => void | Promise<void>;
  export let startInlineSubEdit: (sub: Substitute) => void;
  export let deleteSubstitution: (id: number) => void | Promise<void>;
  export let saveInlineSubEdit: () => void | Promise<void>;
  export let cancelInlineSubEdit: () => void;
</script>

<section class="settings-section">
<header class="content-header"><h2>Substitutions</h2><p class="description">Display substitutions and gags for profile {currentProfile?.name}.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-grid">
    <div class="form-row">
      <input type="text" bind:value={subEditor.pattern} placeholder="Pattern (Regex, $vars literal)" required />
      <input type="text" bind:value={subEditor.replacement} placeholder="Replacement (%0, %1, $vars)" disabled={subEditor.is_gag} />
    </div>
    <div class="form-row">
      <input type="text" bind:value={subEditor.group_name} placeholder="Group Name" />
      <input type="text" bind:value={subPreviewText} placeholder="Preview sample" />
    </div>
    <div class="form-row options-row">
      <label class="checkbox-label"><input type="checkbox" bind:checked={subEditor.enabled} /> Enabled</label>
      <label class="checkbox-label"><input type="checkbox" bind:checked={subEditor.is_gag} /> Gag</label>
      <span class="sub-preview">Preview: {previewSubstitution(subEditor)}</span>
      <button class="btn-primary" on:click={addSubstitution}>
        Add
      </button>
    </div>
  </div>
</div>
<table class="data-table">
  <thead><tr><th style="width: 60px">Order</th><th style="width: 92px">Status</th><th>Pattern</th><th>Replacement</th><th>Group</th><th>Preview</th><th style="width: 140px">Actions</th></tr></thead>
  <tbody>
    {#each subs as sub, i}
      <tr class:gag-row={sub.is_gag}>
        <td class="order-cell">
           <button class="btn-icon" disabled={editingSubID === sub.id || i === 0} on:click={() => moveSubstitution(sub, -1)}>▲</button>
           <button class="btn-icon" disabled={editingSubID === sub.id || i === subs.length - 1} on:click={() => moveSubstitution(sub, 1)}>▼</button>
        </td>
        <td class="flags-cell">
          <div class="flags-wrapper">
            <label class="toggle-label" title="Enabled">
              <input type="checkbox" checked={sub.enabled} disabled={editingSubID === sub.id} on:change={(event) => toggleSubstitution(sub, (event.currentTarget as HTMLInputElement).checked)} />
              <span class={sub.enabled ? 'flag-on' : 'flag-off'}>●</span>
            </label>
            {#if sub.is_gag}<span class="flag-icon" title="Gag">GAG</span>{/if}
          </div>
        </td>
        <td class="key-cell">{sub.pattern}</td>
        <td class="value-cell">{sub.is_gag ? '(hidden)' : sub.replacement}</td>
        <td class="dim-cell">{sub.group_name || '-'}</td>
        <td class="value-cell">{previewSubstitution(sub)}</td>
        <td class="actions-cell">
          {#if editingSubID === sub.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineSubEdit(sub)}>Edit</button>
            {#if sub.id !== undefined}
              <button class="btn-link btn-danger" on:click={() => deleteSubstitution(sub.id!)}>Delete</button>
            {/if}
          {/if}
        </td>
      </tr>
      {#if editingSubID === sub.id}
        <tr class="inline-edit-row">
          <td colspan="7">
            <div class="inline-edit-panel">
              <div class="inline-edit-grid">
                <label>Position <input class="inline-position-input" type="number" bind:value={editingSubDraft.position} aria-label="Substitution position" /></label>
                <label class="inline-field-wide">Pattern <input type="text" bind:value={editingSubDraft.pattern} aria-label="Substitution pattern" /></label>
                <label class="inline-field-wide">Replacement <input type="text" bind:value={editingSubDraft.replacement} aria-label="Substitution replacement" disabled={editingSubDraft.is_gag} /></label>
                <label>Group <input type="text" bind:value={editingSubDraft.group_name} aria-label="Substitution group" /></label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingSubDraft.enabled} /> Enabled</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingSubDraft.is_gag} /> Gag</label>
                <div class="inline-field-wide sub-preview">Preview: {previewSubstitution(editingSubDraft)}</div>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineSubEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineSubEdit}>Cancel</button>
              </div>
              {#if inlineSubError}<div class="form-error inline-error">{inlineSubError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>

<style>
  .sub-preview { color: #9ba3af; font-family: monospace; font-size: 0.85rem; flex: 1; min-width: 180px; }
  .gag-row { background: rgba(231, 76, 60, 0.06); }
</style>
