<script lang="ts">
  import './settings-section.css';
  import { inlineEditKeys } from './inline-edit-keys';
  import type { Profile, Session, SessionProfile } from './types';

  export let formError = '';
  export let sessions: Session[] = [];
  export let sessionEditor: Partial<Session>;
  export let editingSessionID: number | null = null;
  export let editingSessionDraft: Partial<Session>;
  export let inlineSessionError = '';
  export let currentSession: Session | undefined;
  export let sessionProfiles: SessionProfile[] = [];
  export let profiles: Profile[] = [];
  export let sessionProfileToAddID: number | undefined;

  export let addSession: () => void | Promise<void>;
  export let sessionAction: (action: 'connect' | 'disconnect', id: number) => void | Promise<void>;
  export let viewSession: (id: number) => void;
  export let startInlineSessionEdit: (session: Session) => void;
  export let deleteSession: (id: number) => void | Promise<void>;
  export let saveInlineSessionEdit: () => void | Promise<void>;
  export let cancelInlineSessionEdit: () => void;
  export let moveSessionProfile: (index: number, direction: -1 | 1) => void | Promise<void>;
  export let removeProfileFromSession: (profileID: number) => void | Promise<void>;
  export let addProfileToSession: (profileID: number) => void | Promise<void>;
</script>

<section class="settings-section">
<header class="content-header"><h2>Sessions</h2><p class="description">Manage your MUD connections and active profiles.</p></header>
<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
      <input type="text" bind:value={sessionEditor.name} placeholder="Session Name" required />
      <input type="text" bind:value={sessionEditor.mud_host} placeholder="MUD Host" required />
      <input type="number" bind:value={sessionEditor.mud_port} placeholder="Port" required />
      <button class="btn-primary" on:click={addSession}>
          Add
      </button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th style="width: 120px">Status</th><th style="width: 200px">Network</th><th>Name</th><th>Connection</th><th style="width: 240px">Actions</th></tr></thead>
  <tbody>
    {#each sessions as s}
      <tr>
        <td>
          <span class="status-tag {s.status === 'connected' ? 'connected' : 'disconnected'}">
            {s.status}
          </span>
        </td>
        <td>
          {#if s.mccp_active}
            <div class="mccp-stats">
              <span class="mccp-tag">MCCP Active</span>
              <span class="stats-text" title="Compressed / Decompressed">
                {Math.round((s.mccp_compressed_bytes || 0) / 1024)}K / {Math.round((s.mccp_decompressed_bytes || 0) / 1024)}K
              </span>
              <span class="ratio-text">{s.mccp_compression_ratio} saved</span>
            </div>
          {:else}
            <span class="dim-cell">-</span>
          {/if}
        </td>
        <td class="key-cell">{s.name}</td>
        <td class="value-cell">{s.mud_host}:{s.mud_port}</td>
        <td class="actions-cell">
          {#if editingSessionID === s.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            {#if s.status === 'connected'}
                <button class="btn-link btn-danger" on:click={() => sessionAction('disconnect', s.id)}>Disconnect</button>
            {:else}
                <button class="btn-link" on:click={() => sessionAction('connect', s.id)}>Connect</button>
            {/if}
            <button class="btn-link" style="margin-left: 12px" on:click={() => viewSession(s.id)}>View</button>
            <button class="btn-link" style="margin-left: 12px" on:click={() => startInlineSessionEdit(s)}>Edit</button>
            <button class="btn-link btn-danger" style="margin-left: 12px" on:click={() => deleteSession(s.id)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingSessionID === s.id}
        <tr class="inline-edit-row">
          <td colspan="5">
            <div class="inline-edit-panel" use:inlineEditKeys={{ save: saveInlineSessionEdit, cancel: cancelInlineSessionEdit }}>
              <div class="inline-edit-grid">
                <label>Name <input type="text" bind:value={editingSessionDraft.name} aria-label="Session name" /></label>
                <label>MUD Host <input type="text" bind:value={editingSessionDraft.mud_host} aria-label="MUD host" /></label>
                <label>Port <input type="number" bind:value={editingSessionDraft.mud_port} aria-label="MUD port" /></label>
                <label class="inline-field-wide">Initial Commands <textarea bind:value={editingSessionDraft.initial_commands} rows="3" aria-label="Initial commands"></textarea></label>
                <label class="checkbox-label inline-checkbox"><input type="checkbox" checked={editingSessionDraft.mccp_enabled === 1} on:change={(e) => editingSessionDraft = { ...editingSessionDraft, mccp_enabled: (e.currentTarget as HTMLInputElement).checked ? 1 : 0 }} /> Enable MCCP2 compression</label>
                <label>ANSI color theme
                  <select bind:value={editingSessionDraft.ansi_theme} aria-label="ANSI color theme">
                      <option value="classic">Classic (default)</option>
                      <option value="high-contrast">High Contrast</option>
                      <option value="tango-dark">Tango Dark</option>
                      <option value="dracula">Dracula</option>
                      <option value="gruvbox-dark">Gruvbox Dark</option>
                  </select>
                </label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineSessionEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineSessionEdit}>Cancel</button>
              </div>
              {#if inlineSessionError}<div class="form-error inline-error">{inlineSessionError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>

{#if currentSession}
  <h3 style="margin-top: 40px; border-bottom: 1px solid #2d333b; padding-bottom: 8px;">Active Profiles for {currentSession.name}</h3>
  <p class="description">Profiles at the bottom of the list have higher priority and will override rules from profiles above them.</p>
  <table class="data-table" style="margin-bottom: 20px;">
      <thead><tr><th style="width: 60px">Order</th><th>Profile Name</th><th style="width: 120px">Actions</th></tr></thead>
      <tbody>
          {#each sessionProfiles as sp, i}
              <tr>
                  <td class="order-cell">
                      <button class="btn-icon" disabled={i === 0} on:click={() => moveSessionProfile(i, -1)}>▲</button>
                      <button class="btn-icon" disabled={i === sessionProfiles.length - 1} on:click={() => moveSessionProfile(i, 1)}>▼</button>
                  </td>
                  <td class="value-cell">{sp.profile_name}</td>
                  <td class="actions-cell">
                      <button class="btn-link btn-danger" on:click={() => removeProfileFromSession(sp.profile_id)}>Remove</button>
                  </td>
              </tr>
          {/each}
          {#if sessionProfiles.length === 0}
              <tr><td colspan="3" class="dim-cell">No active profiles for this session.</td></tr>
          {/if}
      </tbody>
  </table>

  <div class="form-row" style="max-width: 400px">
      <select bind:value={sessionProfileToAddID} style="flex: 1; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px; border-radius: 6px;">
          <option value={undefined} disabled selected>Select a profile to add</option>
          {#each profiles as p}
              <option value={p.id}>{p.name}</option>
          {/each}
      </select>
      <button class="btn-primary" disabled={!sessionProfileToAddID} on:click={() => { if (sessionProfileToAddID) addProfileToSession(sessionProfileToAddID); sessionProfileToAddID = undefined; }}>Add</button>
  </div>
{/if}
</section>

<style>
  .status-tag { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; font-weight: bold; text-transform: uppercase; }
  .status-tag.connected { background: rgba(46, 204, 113, 0.2); color: #2ecc71; border: 1px solid rgba(46, 204, 113, 0.3); }
  .status-tag.disconnected { background: rgba(231, 76, 60, 0.2); color: #e74c3c; border: 1px solid rgba(231, 76, 60, 0.3); }
  .mccp-stats { display: flex; flex-direction: column; gap: 4px; }
  .mccp-tag { align-self: flex-start; padding: 2px 6px; border-radius: 4px; font-size: 0.65rem; font-weight: bold; background: rgba(52, 152, 219, 0.2); color: #3498db; border: 1px solid rgba(52, 152, 219, 0.3); text-transform: uppercase; }
  .stats-text { font-size: 0.75rem; color: #9ba3af; font-family: monospace; }
  .ratio-text { font-size: 0.7rem; color: #2ecc71; font-weight: bold; }
  .inline-edit-grid select, .inline-edit-grid textarea { width: 100%; box-sizing: border-box; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px 12px; border-radius: 6px; }
  .inline-edit-grid textarea { resize: vertical; font-family: monospace; }
</style>
