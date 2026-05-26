<script lang="ts">
  import './settings-section.css';
  import type { Profile, Trigger } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let triggers: Trigger[] = [];
  export let triggerEditor: Trigger;
  export let editingTriggerID: number | null = null;
  export let editingTriggerDraft: Trigger;
  export let inlineTriggerError = '';

  export let addTrigger: () => void | Promise<void>;
  export let moveTrigger: (trigger: Trigger, direction: -1 | 1) => void | Promise<void>;
  export let toggleTrigger: (trigger: Trigger, enabled: boolean) => void | Promise<void>;
  export let startInlineTriggerEdit: (trigger: Trigger) => void;
  export let deleteTrigger: (id: number) => void | Promise<void>;
  export let saveInlineTriggerEdit: () => void | Promise<void>;
  export let cancelInlineTriggerEdit: () => void;
</script>

<section class="settings-section">
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
      <button class="btn-primary" on:click={addTrigger}>
        Add
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
           <button class="btn-icon" disabled={editingTriggerID === t.id || i === 0} on:click={() => moveTrigger(t, -1)}>▲</button>
           <button class="btn-icon" disabled={editingTriggerID === t.id || i === triggers.length - 1} on:click={() => moveTrigger(t, 1)}>▼</button>
        </td>
        <td class="flags-cell">
          <div class="flags-wrapper">
            <label class="toggle-label" title="Enabled">
              <input type="checkbox" checked={t.enabled} disabled={editingTriggerID === t.id} on:change={(event) => toggleTrigger(t, (event.currentTarget as HTMLInputElement).checked)} />
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
          {#if editingTriggerID === t.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineTriggerEdit(t)}>Edit</button>
            <button class="btn-link btn-danger" on:click={() => deleteTrigger(t.id!)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingTriggerID === t.id}
        <tr class="inline-edit-row">
          <td colspan="6">
            <div class="inline-edit-panel">
              <div class="inline-edit-grid">
                <label>Position <input class="inline-position-input" type="number" bind:value={editingTriggerDraft.position} aria-label="Trigger position" /></label>
                <label>Name <input type="text" bind:value={editingTriggerDraft.name} aria-label="Trigger name" placeholder="Name" /></label>
                <label class="inline-field-wide">Pattern <input type="text" bind:value={editingTriggerDraft.pattern} aria-label="Trigger pattern" /></label>
                <label class="inline-field-wide">Command <input type="text" bind:value={editingTriggerDraft.command} aria-label="Trigger command" /></label>
                <label>Group <input type="text" bind:value={editingTriggerDraft.group_name} aria-label="Trigger group" /></label>
                <label>Routing
                  <select bind:value={editingTriggerDraft.buffer_action} aria-label="Trigger buffer action">
                    <option value="">No Routing</option>
                    <option value="move">Move</option>
                    <option value="copy">Copy</option>
                    <option value="echo">Echo</option>
                  </select>
                </label>
                <label>Target Buffer <input type="text" bind:value={editingTriggerDraft.target_buffer} aria-label="Trigger target buffer" placeholder="Target Buffer" disabled={!editingTriggerDraft.buffer_action} /></label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingTriggerDraft.enabled} /> Enabled</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingTriggerDraft.is_button} /> Button</label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" bind:checked={editingTriggerDraft.stop_after_match} /> Stop after match</label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineTriggerEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineTriggerEdit}>Cancel</button>
              </div>
              {#if inlineTriggerError}<div class="form-error inline-error">{inlineTriggerError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>

<style>
  .comment-text { font-size: 0.75rem; color: #8e96a3; margin-top: 2px; font-style: italic; opacity: 0.8; }
  .inline-edit-grid select { width: 100%; box-sizing: border-box; }
</style>
