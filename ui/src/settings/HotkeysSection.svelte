<script lang="ts">
  import './settings-section.css';
  import type { Hotkey, Profile } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let hotkeys: Hotkey[] = [];
  export let hotkeyEditor: Hotkey;

  export let saveHotkey: () => void | Promise<void>;
  export let moveHotkey: (hotkey: Hotkey, direction: -1 | 1) => void | Promise<void>;
  export let editHotkey: (hotkey: Hotkey) => void;
  export let deleteHotkey: (id: number) => void | Promise<void>;
</script>

<section class="settings-section">
<header class="content-header"><h2>Hotkeys</h2><p class="description">Keyboard shortcuts for profile {currentProfile?.name}.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
    <input type="text" bind:value={hotkeyEditor.shortcut} placeholder="Shortcut (e.g. F1, Ctrl+A)" required />
    <input type="text" bind:value={hotkeyEditor.command} placeholder="Command" required />
    <div class="form-row-group">
      <label class="compact-label">Mob Row: <input type="number" bind:value={hotkeyEditor.mobile_row} min="0" style="width: 60px" /></label>
      <label class="compact-label">Order: <input type="number" bind:value={hotkeyEditor.mobile_order} min="0" style="width: 60px" /></label>
    </div>
    <button class="btn-primary" on:click={saveHotkey}>
      {hotkeyEditor.id ? 'Update' : 'Add'}
    </button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th style="width: 60px">Order</th><th>Shortcut</th><th>Command</th><th style="width: 80px">Mob Row</th><th style="width: 80px">Mob Ord</th><th style="width: 140px">Actions</th></tr></thead>
  <tbody>
    {#each hotkeys as h, i}
      <tr>
        <td class="order-cell">
           <button class="btn-icon" disabled={i === 0} on:click={() => moveHotkey(h, -1)}>▲</button>
           <button class="btn-icon" disabled={i === hotkeys.length - 1} on:click={() => moveHotkey(h, 1)}>▼</button>
        </td>
        <td class="key-cell">{h.shortcut}</td><td class="value-cell">{h.command}</td>
        <td class="dim-cell center-cell">{h.mobile_row || '-'}</td>
        <td class="dim-cell center-cell">{h.mobile_order || '-'}</td>
        <td class="actions-cell">
          <button class="btn-link" on:click={() => editHotkey(h)}>Edit</button>
          <button class="btn-link btn-danger" on:click={() => deleteHotkey(h.id!)}>Delete</button>
        </td>
      </tr>
    {/each}
  </tbody>
</table>
</section>

<style>
  .form-row-group { display: flex; gap: 12px; align-items: center; background: #111315; padding: 4px 12px; border-radius: 6px; border: 1px solid #30363d; }
  .compact-label { font-size: 0.75rem; color: #9ba3af; white-space: nowrap; display: flex; align-items: center; gap: 6px; }
  .center-cell { text-align: center; }
</style>
