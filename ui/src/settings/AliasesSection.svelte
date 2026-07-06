<script lang="ts">
  import './settings-section.css';
  import { inlineEditKeys } from './inline-edit-keys';
  import type { Alias, Profile } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let aliases: Alias[] = [];
  export let aliasEditor: Alias;
  export let editingAliasID: number | null = null;
  export let editingAliasDraft: Alias;
  export let inlineAliasError = '';

  export let addAlias: () => void | Promise<void>;
  export let moveAlias: (alias: Alias, direction: -1 | 1) => void | Promise<void>;
  export let toggleAlias: (alias: Alias, enabled: boolean) => void | Promise<void>;
  export let startInlineAliasEdit: (alias: Alias) => void;
  export let deleteAlias: (id: number) => void | Promise<void>;
  export let saveInlineAliasEdit: () => void | Promise<void>;
  export let cancelInlineAliasEdit: () => void;
</script>

<section class="settings-section">
<header class="content-header"><h2>Aliases</h2><p class="description">Command shortcuts for profile {currentProfile?.name}.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
    <input type="text" bind:value={aliasEditor.name} placeholder="Alias Name" required />
    <input type="text" bind:value={aliasEditor.template} placeholder="Template" required />
    <input type="text" bind:value={aliasEditor.group_name} placeholder="Group Name" />
    <label class="checkbox-label"><input type="checkbox" bind:checked={aliasEditor.enabled} /> Enabled</label>
    <button class="btn-primary" on:click={addAlias}>
      Add
    </button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th style="width: 60px">Order</th><th style="width: 72px">Status</th><th>Name</th><th>Template</th><th>Group</th><th style="width: 140px">Actions</th></tr></thead>
  <tbody>
    {#each aliases as a, i}
      <tr>
        <td class="order-cell">
           <button class="btn-icon" disabled={editingAliasID === a.id || i === 0} on:click={() => moveAlias(a, -1)}>▲</button>
           <button class="btn-icon" disabled={editingAliasID === a.id || i === aliases.length - 1} on:click={() => moveAlias(a, 1)}>▼</button>
        </td>
        <td>
          <label class="toggle-label">
            <input type="checkbox" checked={a.enabled} disabled={editingAliasID === a.id} on:change={(event) => toggleAlias(a, (event.currentTarget as HTMLInputElement).checked)} />
            <span class="status-dot {a.enabled ? 'on' : 'off'}" title={a.enabled ? 'Enabled' : 'Disabled'}></span>
          </label>
        </td>
        <td class="key-cell">{a.name}</td><td class="value-cell">{a.template}</td>
        <td class="dim-cell">{a.group_name || '-'}</td>
        <td class="actions-cell">
          {#if editingAliasID === a.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineAliasEdit(a)}>Edit</button>
            {#if a.id !== undefined}
              <button class="btn-link btn-danger" on:click={() => deleteAlias(a.id!)}>Delete</button>
            {/if}
          {/if}
        </td>
      </tr>
      {#if editingAliasID === a.id}
        <tr class="inline-edit-row">
          <td colspan="6">
            <div class="inline-edit-panel" use:inlineEditKeys={{ save: saveInlineAliasEdit, cancel: cancelInlineAliasEdit }}>
              <div class="inline-edit-grid">
                <label>Position <input class="inline-position-input" type="number" bind:value={editingAliasDraft.position} aria-label="Alias position" /></label>
                <label>Name <input type="text" bind:value={editingAliasDraft.name} aria-label="Alias name" /></label>
                <label class="inline-field-wide">Template <input type="text" bind:value={editingAliasDraft.template} aria-label="Alias template" /></label>
                <label>Group <input type="text" bind:value={editingAliasDraft.group_name} aria-label="Alias group" /></label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingAliasDraft.enabled} /> Enabled</label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineAliasEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineAliasEdit}>Cancel</button>
              </div>
              {#if inlineAliasError}<div class="form-error inline-error">{inlineAliasError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>

<style>
  .status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; vertical-align: middle; }
  .status-dot.on { background: #2ecc71; box-shadow: 0 0 5px #2ecc71; }
  .status-dot.off { background: #95a5a6; }
</style>
