<script lang="ts">
  import './settings-section.css';
  import { inlineEditKeys } from './inline-edit-keys';
  import type { Profile, Session, SessionProfile } from './types';
  import { fetchMapSets, fetchActiveMapSet, saveActiveMapSet } from './api';

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

  function detectBrowserTimezone(): string {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    } catch {
      return '';
    }
  }

  const browserTimezone = detectBrowserTimezone();

  function supportedTimezones(): string[] {
    let zones: string[] = [];
    try {
      // @ts-ignore - supportedValuesOf is not yet in older TS lib defs
      if (typeof Intl.supportedValuesOf === 'function') {
        // @ts-ignore
        zones = Intl.supportedValuesOf('timeZone') as string[];
      }
    } catch {
      zones = [];
    }
    if (zones.length === 0) {
      zones = ['UTC', 'Europe/London', 'Europe/Kyiv', 'America/New_York', 'America/Los_Angeles', 'Asia/Tokyo', 'Australia/Sydney'];
    }
    return zones;
  }

  const baseTimezones = supportedTimezones();

  // Ensure the currently-selected zone, the browser zone and UTC are always
  // available as options even if not present in the platform list.
  $: timezoneOptions = Array.from(
    new Set(['UTC', browserTimezone, editingSessionDraft.timezone, ...baseTimezones].filter((z): z is string => !!z))
  );

  function selectTimezone(value: string) {
    // Choosing a specific zone pins it (stop following the browser).
    editingSessionDraft = { ...editingSessionDraft, timezone: value, tz_follow: 0 };
  }

  function toggleFollowTimezone(follow: boolean) {
    editingSessionDraft = {
      ...editingSessionDraft,
      tz_follow: follow ? 1 : 0,
      // Re-enabling follow adopts the current browser zone immediately.
      timezone: follow && browserTimezone ? browserTimezone : editingSessionDraft.timezone,
    };
  }

  // --- Map set selector (§6a) ------------------------------------------------
  // The active map set is a session column managed outside the session record
  // (see /api/sessions/{id}/active-map-set), so it has its own small state here.
  let mapSets: Array<{ id: number; name: string; zone_count: number; room_count: number }> = [];
  let activeMapSetID: number | null = null;
  let mapSetLoadedFor: number | null = null;
  let mapSetSaving = false;
  let mapSetError = '';

  async function loadMapSetState(sessionID: number) {
    mapSetError = '';
    try {
      const [sets, active] = await Promise.all([fetchMapSets(), fetchActiveMapSet(sessionID)]);
      mapSets = sets || [];
      activeMapSetID = active?.active_map_set_id ?? null;
      mapSetLoadedFor = sessionID;
    } catch (err) {
      mapSetError = err instanceof Error ? err.message : String(err);
    }
  }

  // Load whenever the viewed session changes.
  $: if (currentSession && currentSession.id !== mapSetLoadedFor) {
    void loadMapSetState(currentSession.id);
  }

  async function onMapSetChange(sessionID: number, value: string) {
    const next = value === '' ? null : Number(value);
    mapSetSaving = true;
    mapSetError = '';
    try {
      const resp = await saveActiveMapSet(sessionID, next);
      if (!resp.ok) throw new Error(await resp.text());
      activeMapSetID = next;
    } catch (err) {
      mapSetError = err instanceof Error ? err.message : String(err);
    } finally {
      mapSetSaving = false;
    }
  }
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
                <label class="checkbox-label inline-checkbox">
                  <input
                    type="checkbox"
                    checked={editingSessionDraft.tz_follow === 1}
                    on:change={(e) => toggleFollowTimezone((e.currentTarget as HTMLInputElement).checked)}
                  />
                  Follow browser timezone{browserTimezone ? ` (${browserTimezone})` : ''}
                </label>
                <label>Timezone
                  <select
                    value={editingSessionDraft.timezone || 'UTC'}
                    on:change={(e) => selectTimezone((e.currentTarget as HTMLSelectElement).value)}
                    aria-label="Session timezone"
                  >
                    {#each timezoneOptions as tz}
                      <option value={tz}>{tz}</option>
                    {/each}
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

  <h3 style="margin-top: 40px; border-bottom: 1px solid #2d333b; padding-bottom: 8px;">Map Set for {currentSession.name}</h3>
  <p class="description">Choose the world map this session follows. The <code>~map</code> buffer renders it and tracks the player's position.</p>
  <div class="form-row" style="max-width: 400px">
    <select
      style="flex: 1; background: #0d1117; border: 1px solid #30363d; color: #e8edf2; padding: 8px; border-radius: 6px;"
      disabled={mapSetSaving}
      value={activeMapSetID === null ? '' : String(activeMapSetID)}
      on:change={(e) => currentSession && onMapSetChange(currentSession.id, (e.currentTarget as HTMLSelectElement).value)}
    >
      <option value="">(none)</option>
      {#each mapSets as ms}
        <option value={String(ms.id)}>{ms.name} — {ms.zone_count} zones, {ms.room_count} rooms</option>
      {/each}
    </select>
  </div>
  {#if mapSets.length === 0}
    <p class="description">No map sets imported yet.</p>
  {/if}
  {#if mapSetError}
    <p class="description" style="color: #e74c3c;">Map set error: {mapSetError}</p>
  {/if}
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
