<script lang="ts">
  import './settings-section.css';
  import { inlineEditKeys } from './inline-edit-keys';
  import { formatKeyboardShortcut } from './shortcut';
  import type { Hotkey, Profile } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let hotkeys: Hotkey[] = [];
  export let hotkeyEditor: Hotkey;
  export let editingHotkeyID: number | null = null;
  export let editingHotkeyDraft: Hotkey;
  export let inlineHotkeyError = '';

  export let addHotkey: () => void | Promise<void>;
  export let moveHotkey: (hotkey: Hotkey, direction: -1 | 1) => void | Promise<void>;
  export let startInlineHotkeyEdit: (hotkey: Hotkey) => void;
  export let deleteHotkey: (id: number) => void | Promise<void>;
  export let saveInlineHotkeyEdit: () => void | Promise<void>;
  export let cancelInlineHotkeyEdit: () => void;

  const NEW_HOTKEY_CAPTURE_ID = -1;

  let capturingHotkeyID: number | null = null;
  let lastEditingHotkeyID: number | null = null;

  $: if (editingHotkeyID !== lastEditingHotkeyID) {
    capturingHotkeyID = null;
    lastEditingHotkeyID = editingHotkeyID;
  }

  function startCapture(hotkeyID: number | null) {
    if (hotkeyID === null) return;
    capturingHotkeyID = hotkeyID;
  }

  function cancelCapture() {
    capturingHotkeyID = null;
  }

  function handleCaptureKeydown(event: KeyboardEvent) {
    if (capturingHotkeyID === null) return;
    event.preventDefault();
    event.stopPropagation();
    const shortcut = formatKeyboardShortcut(event);
    if (!shortcut) return;
    if (capturingHotkeyID === NEW_HOTKEY_CAPTURE_ID) {
      hotkeyEditor = { ...hotkeyEditor, shortcut };
    } else if (capturingHotkeyID === editingHotkeyID) {
      editingHotkeyDraft = { ...editingHotkeyDraft, shortcut };
    }
    capturingHotkeyID = null;
  }
</script>

<svelte:window on:keydown={handleCaptureKeydown} />

<section class="settings-section">
<header class="content-header"><h2>Hotkeys</h2><p class="description">Keyboard shortcuts for profile {currentProfile?.name}. Saved changes sync to the game client automatically.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
    <div class="shortcut-add-field">
      <input type="text" bind:value={hotkeyEditor.shortcut} placeholder="Shortcut (e.g. F1, Ctrl+A)" required />
      <button class="btn-secondary" type="button" on:click={() => startCapture(NEW_HOTKEY_CAPTURE_ID)}>{capturingHotkeyID === NEW_HOTKEY_CAPTURE_ID ? 'Capturing…' : 'Capture Shortcut'}</button>
      {#if capturingHotkeyID === NEW_HOTKEY_CAPTURE_ID}
        <span class="capture-hint">Press a shortcut now. Modifier-only keys are ignored.</span>
        <button class="btn-link" type="button" on:click={cancelCapture}>Cancel capture</button>
      {/if}
    </div>
    <input type="text" bind:value={hotkeyEditor.command} placeholder="Command" required />
    <div class="form-row-group">
      <label class="compact-label">Mob Row: <input type="number" bind:value={hotkeyEditor.mobile_row} min="0" style="width: 60px" /></label>
      <label class="compact-label">Order: <input type="number" bind:value={hotkeyEditor.mobile_order} min="0" style="width: 60px" /></label>
    </div>
    <button class="btn-primary" on:click={() => { cancelCapture(); addHotkey(); }}>
      Add
    </button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th style="width: 60px">Order</th><th>Shortcut</th><th>Command</th><th style="width: 80px">Mob Row</th><th style="width: 80px">Mob Ord</th><th style="width: 140px">Actions</th></tr></thead>
  <tbody>
    {#each hotkeys as h, i}
      <tr>
        <td class="order-cell">
           <button class="btn-icon" disabled={editingHotkeyID === h.id || i === 0} on:click={() => moveHotkey(h, -1)}>▲</button>
           <button class="btn-icon" disabled={editingHotkeyID === h.id || i === hotkeys.length - 1} on:click={() => moveHotkey(h, 1)}>▼</button>
        </td>
        <td class="key-cell">{h.shortcut}</td><td class="value-cell">{h.command}</td>
        <td class="dim-cell center-cell">{h.mobile_row || '-'}</td>
        <td class="dim-cell center-cell">{h.mobile_order || '-'}</td>
        <td class="actions-cell">
          {#if editingHotkeyID === h.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => startInlineHotkeyEdit(h)}>Edit</button>
            <button class="btn-link btn-danger" on:click={() => deleteHotkey(h.id!)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingHotkeyID === h.id}
        <tr class="inline-edit-row">
          <td colspan="6">
            <div class="inline-edit-panel" use:inlineEditKeys={{ save: saveInlineHotkeyEdit, cancel: cancelInlineHotkeyEdit }}>
              <div class="inline-edit-grid">
                <label>Shortcut <input type="text" bind:value={editingHotkeyDraft.shortcut} aria-label="Hotkey shortcut" /></label>
                <div class="capture-panel">
                  <button class="btn-secondary" type="button" on:click={() => startCapture(editingHotkeyID)}>{capturingHotkeyID === h.id ? 'Capturing…' : 'Capture Shortcut'}</button>
                  {#if capturingHotkeyID === h.id}
                    <span class="capture-hint">Press a shortcut now. Modifier-only keys are ignored.</span>
                    <button class="btn-link" type="button" on:click={cancelCapture}>Cancel capture</button>
                  {/if}
                </div>
                <label class="inline-field-wide">Command <input type="text" bind:value={editingHotkeyDraft.command} aria-label="Hotkey command" /></label>
                <label>Mob Row <input type="number" bind:value={editingHotkeyDraft.mobile_row} min="0" aria-label="Hotkey mobile row" /></label>
                <label>Order <input type="number" bind:value={editingHotkeyDraft.mobile_order} min="0" aria-label="Hotkey mobile order" /></label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={() => { cancelCapture(); saveInlineHotkeyEdit(); }}>Save</button>
                <button class="btn-link" on:click={() => { cancelCapture(); cancelInlineHotkeyEdit(); }}>Cancel</button>
              </div>
              {#if inlineHotkeyError}<div class="form-error inline-error">{inlineHotkeyError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>
</section>

<style>
  .form-row-group { display: flex; gap: 12px; align-items: center; background: #111315; padding: 4px 12px; border-radius: 6px; border: 1px solid #30363d; }
  .shortcut-add-field { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .compact-label { font-size: 0.75rem; color: #9ba3af; white-space: nowrap; display: flex; align-items: center; gap: 6px; }
  .center-cell { text-align: center; }
  .capture-panel { display: flex; align-items: center; gap: 10px; min-height: 36px; flex-wrap: wrap; }
  .capture-hint { color: #9ba3af; font-size: 0.85rem; }
  .btn-secondary { background: #30363d; color: #e8edf2; border: 1px solid #30363d; padding: 8px 12px; border-radius: 6px; cursor: pointer; }
  .btn-secondary:hover { background: #3c444d; }
</style>
