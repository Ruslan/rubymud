<script lang="ts">
  import './settings-section.css';
  import { inlineEditKeys } from './inline-edit-keys';
  import type { Profile, ProfileVariable } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let profileVariables: ProfileVariable[] = [];
  export let profileVariableEditor: ProfileVariable;
  export let editingProfileVariableID: number | null = null;
  export let editingProfileVariableDraft: ProfileVariable;
  export let inlineProfileVariableError = '';

  export let addProfileVariable: () => void | Promise<void>;
  export let moveProfileVariable: (variable: ProfileVariable, direction: -1 | 1) => void | Promise<void>;
  export let startInlineProfileVariableEdit: (variable: ProfileVariable) => void;
  export let deleteProfileVariable: (id: number) => void | Promise<void>;
  export let saveInlineProfileVariableEdit: () => void | Promise<void>;
  export let cancelInlineProfileVariableEdit: () => void;
</script>

<section class="settings-section">
<header class="content-header"><h2>Declared Variables</h2><p class="description">Variables declared by profile {currentProfile?.name}.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
    <input type="text" bind:value={profileVariableEditor.name} placeholder="Name" required />
    <input type="text" bind:value={profileVariableEditor.default_value} placeholder="Default Value" />
    <input type="text" bind:value={profileVariableEditor.description} placeholder="Description" />
    <button class="btn-primary" on:click={addProfileVariable}>
      Add
    </button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th style="width: 60px">Order</th><th>Name</th><th>Default Value</th><th>Description</th><th style="width: 140px">Actions</th></tr></thead>
  <tbody>
    {#each profileVariables as v, i}
      <tr>
        <td class="order-cell">
           <button class="btn-icon" disabled={editingProfileVariableID === v.id || i === 0} on:click={() => moveProfileVariable(v, -1)}>▲</button>
           <button class="btn-icon" disabled={editingProfileVariableID === v.id || i === profileVariables.length - 1} on:click={() => moveProfileVariable(v, 1)}>▼</button>
        </td>
        <td class="key-cell">{v.name}</td><td class="value-cell">{v.default_value || '-'}</td>
        <td class="dim-cell">{v.description || '-'}</td>
        <td class="actions-cell">
          {#if editingProfileVariableID === v.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineProfileVariableEdit(v)}>Edit</button>
            <button class="btn-link btn-danger" on:click={() => deleteProfileVariable(v.id!)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingProfileVariableID === v.id}
        <tr class="inline-edit-row">
          <td colspan="5">
            <div class="inline-edit-panel" use:inlineEditKeys={{ save: saveInlineProfileVariableEdit, cancel: cancelInlineProfileVariableEdit }}>
              <div class="inline-edit-grid">
                <label>Position <input class="inline-position-input" type="number" bind:value={editingProfileVariableDraft.position} aria-label="Declared variable position" /></label>
                <label>Name <input type="text" bind:value={editingProfileVariableDraft.name} aria-label="Declared variable name" /></label>
                <label class="inline-field-wide">Default Value <input type="text" bind:value={editingProfileVariableDraft.default_value} aria-label="Declared variable default value" /></label>
                <label class="inline-field-wide">Description <input type="text" bind:value={editingProfileVariableDraft.description} aria-label="Declared variable description" /></label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineProfileVariableEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineProfileVariableEdit}>Cancel</button>
              </div>
              {#if inlineProfileVariableError}<div class="form-error inline-error">{inlineProfileVariableError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>
