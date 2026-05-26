<script lang="ts">
  import './settings-section.css';
  import type { Session, Variable } from './types';

  export let currentSession: Session | undefined;
  export let formError = '';
  export let variables: Variable[] = [];
  export let newVarKey = '';
  export let newVarValue = '';
  export let editingVariableKey: string | null = null;
  export let editingVariableDraft: Variable;
  export let inlineVariableError = '';

  export let addVariable: () => void | Promise<void>;
  export let startInlineVariableEdit: (variable: Variable) => void;
  export let deleteVariable: (key: string) => void | Promise<void>;
  export let saveInlineVariableEdit: () => void | Promise<void>;
  export let cancelInlineVariableEdit: () => void;
</script>

<section class="settings-section">
<header class="content-header"><h2>Variables</h2><p class="description">State variables for {currentSession?.name || 'selected session'}.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
    <input type="text" bind:value={newVarKey} placeholder="Key" required />
    <input type="text" bind:value={newVarValue} placeholder="Value" />
    <button class="btn-primary" on:click={addVariable}>Save</button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th>Key</th><th>Value</th><th style="width: 140px">Actions</th></tr></thead>
  <tbody>
    {#each variables as v}
      <tr>
        <td class="key-cell">{v.key}</td><td class="value-cell">{v.value}</td>
        <td class="actions-cell">
          {#if editingVariableKey === v.key}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineVariableEdit(v)}>Edit</button>
            <button class="btn-link btn-danger" on:click={() => deleteVariable(v.key)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingVariableKey === v.key}
        <tr class="inline-edit-row">
          <td colspan="3">
            <div class="inline-edit-panel">
              <div class="inline-edit-grid">
                <label>Key <input type="text" bind:value={editingVariableDraft.key} aria-label="Variable key" /></label>
                <label class="inline-field-wide">Value <input type="text" bind:value={editingVariableDraft.value} aria-label="Variable value" /></label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineVariableEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineVariableEdit}>Cancel</button>
              </div>
              {#if inlineVariableError}<div class="form-error inline-error">{inlineVariableError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>
