<script lang="ts">
  import './settings-section.css';
  import type { Profile, ProfileTimer } from './types';

  export let profiles: Profile[] = [];
  export let allProfilesTimers: Record<number, ProfileTimer[]> = {};
  export let expandedTimerSubs: Record<string, boolean> = {};
  export let newTimerForms: Record<number, { name: string; cycle_ms: number; icon: string; repeat_mode: string }> = {};
  export let newSubForms: Record<string, { second: number; command: string; is_removal: boolean; is_bulk: boolean }> = {};

  export let saveProfileTimer: (profileID: number, timer: any) => void | Promise<void>;
  export let deleteProfileTimer: (profileID: number, name: string) => void | Promise<void>;
  export let saveProfileTimerSubscription: (profileID: number, timerName: string, sub: any) => void | Promise<void>;
  export let deleteProfileTimerSubscription: (profileID: number, timerName: string, sub: any) => void | Promise<void>;
  export let addSub: (profileID: number, timerName: string) => void | Promise<void>;
  export let addTimer: (profileID: number) => void | Promise<void>;

  function toggleTimerSubs(profileID: number, timerName: string) {
    const key = `${profileID}|${timerName}`;
    expandedTimerSubs = { ...expandedTimerSubs, [key]: !expandedTimerSubs[key] };
  }
</script>

<section class="settings-section">
<header class="content-header">
  <h2>Tickers</h2>
  <p class="description">Manage timer declarations and tick subscriptions (#tickat) across all your profiles.</p>
</header>

{#each profiles as profile}
  <div class="profile-timers-card">
    <div class="profile-timers-header">
      <h3>{profile.name} <span class="profile-badge">ID: {profile.id}</span></h3>
      {#if profile.description}
        <p class="profile-desc">{profile.description}</p>
      {/if}
    </div>

    <table class="data-table">
      <thead>
        <tr>
          <th style="width: 25%;">Ticker Name</th>
          <th style="width: 20%;">Cycle (ms)</th>
          <th style="width: 15%;">Icon</th>
          <th style="width: 20%;">Repeat Mode</th>
          <th style="text-align: right; width: 20%;">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each allProfilesTimers[profile.id] || [] as timer}
          <!-- Main Ticker Row -->
          <tr class="ticker-main-row">
            <td class="key-cell font-monospace">
              <span class="ticker-name-label">{timer.name}</span>
            </td>
            <td>
              <input type="number" class="inline-input" placeholder="1000" bind:value={timer.cycle_ms} on:blur={() => saveProfileTimer(profile.id, timer)} />
            </td>
            <td>
              <input type="text" class="inline-input" placeholder="Icon" bind:value={timer.icon} on:blur={() => saveProfileTimer(profile.id, timer)} />
            </td>
            <td>
              <select class="inline-select" bind:value={timer.repeat_mode} on:change={() => saveProfileTimer(profile.id, timer)}>
                <option value="repeating">Repeating</option>
                <option value="one_shot">Once</option>
              </select>
            </td>
            <td style="text-align: right; white-space: nowrap;">
              <button class="btn-link" on:click={() => toggleTimerSubs(profile.id, timer.name)}>
                {expandedTimerSubs[`${profile.id}|${timer.name}`] ? '▲ Hide Subs' : '▼ Show Subs'} ({timer.subscriptions?.length || 0})
              </button>
              <button class="btn-link btn-danger" style="margin-left: 8px;" on:click={() => deleteProfileTimer(profile.id, timer.name)}>Delete</button>
            </td>
          </tr>

          <!-- Nested Subscriptions Table Row -->
          {#if expandedTimerSubs[`${profile.id}|${timer.name}`]}
            <tr class="nested-row">
              <td colspan="5">
                <div class="nested-table-container">
                  <div class="nested-table-header">
                    <h4>Subscriptions for Ticker: <span class="highlight-text">{timer.name}</span></h4>
                  </div>
                  <table class="nested-data-table">
                    <thead>
                      <tr>
                        <th style="width: 15%;">Second</th>
                        <th style="width: 50%;">Command</th>
                        <th style="width: 15%; text-align: center;">Removal</th>
                        <th style="width: 10%; text-align: center;">Bulk</th>
                        <th style="width: 10%; text-align: right;">Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each timer.subscriptions || [] as sub}
                        <tr class="nested-tr">
                          <td>
                            <span class="key-cell" style="padding-left: 10px;">{sub.second}</span>
                          </td>
                          <td>
                            <input type="text" class="inline-input font-monospace" placeholder="Command" bind:value={sub.command} on:blur={() => saveProfileTimerSubscription(profile.id, timer.name, sub)} />
                          </td>
                          <td style="text-align: center;">
                            <input type="checkbox" bind:checked={sub.is_removal} on:change={() => saveProfileTimerSubscription(profile.id, timer.name, sub)} />
                          </td>
                          <td style="text-align: center;">
                            <input type="checkbox" bind:checked={sub.is_bulk} on:change={() => saveProfileTimerSubscription(profile.id, timer.name, sub)} />
                          </td>
                          <td style="text-align: right;">
                            <button class="btn-link btn-danger" on:click={() => deleteProfileTimerSubscription(profile.id, timer.name, sub)}>Delete</button>
                          </td>
                        </tr>
                      {/each}

                      <!-- Inline Row to Add Subscription -->
                      {#if newSubForms[`${profile.id}|${timer.name}`]}
                      <tr class="nested-tr add-sub-tr">
                        <td>
                          <input type="number" class="inline-input new-input" placeholder="0" bind:value={newSubForms[`${profile.id}|${timer.name}`].second} />
                        </td>
                        <td>
                          <input type="text" class="inline-input new-input font-monospace" placeholder="#echo Tick command" bind:value={newSubForms[`${profile.id}|${timer.name}`].command} />
                        </td>
                        <td style="text-align: center;">
                          <input type="checkbox" bind:checked={newSubForms[`${profile.id}|${timer.name}`].is_removal} />
                        </td>
                        <td style="text-align: center;">
                          <input type="checkbox" bind:checked={newSubForms[`${profile.id}|${timer.name}`].is_bulk} />
                        </td>
                        <td style="text-align: right;">
                          <button class="btn-primary btn-xs" on:click={() => addSub(profile.id, timer.name)}>+ Add Sub</button>
                        </td>
                      </tr>
                      {/if}
                    </tbody>
                  </table>
                </div>
              </td>
            </tr>
          {/if}
        {:else}
          <tr>
            <td colspan="5" class="dim-cell text-center" style="padding: 16px;">No tickers declared in this profile.</td>
          </tr>
        {/each}

        <!-- Add Ticker Row at the bottom of the table -->
        {#if newTimerForms[profile.id]}
        <tr class="add-ticker-tr">
          <td>
            <input type="text" class="inline-input new-input font-monospace" placeholder="Name (e.g. herb)" bind:value={newTimerForms[profile.id].name} />
          </td>
          <td>
            <input type="number" class="inline-input new-input" placeholder="1000" bind:value={newTimerForms[profile.id].cycle_ms} />
          </td>
          <td>
            <input type="text" class="inline-input new-input" placeholder="Icon (optional)" bind:value={newTimerForms[profile.id].icon} />
          </td>
          <td>
            <select class="inline-select new-input" bind:value={newTimerForms[profile.id].repeat_mode}>
              <option value="repeating">Repeating</option>
              <option value="one_shot">Once</option>
            </select>
          </td>
          <td style="text-align: right;">
            <button class="btn-primary btn-sm" on:click={() => addTimer(profile.id)}>+ Add Ticker</button>
          </td>
        </tr>
        {/if}
      </tbody>
    </table>
  </div>
{/each}
</section>

<style>
  .profile-timers-card {
    background: #1a1d21;
    border: 1px solid #30363d;
    border-radius: 8px;
    padding: 20px;
    margin-bottom: 24px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.15);
  }
  .profile-timers-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid #30363d;
    padding-bottom: 12px;
    margin-bottom: 16px;
  }
  .profile-timers-header h3 {
    margin: 0;
    font-size: 1.2rem;
    color: #e8edf2;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .profile-badge {
    font-size: 0.7rem;
    padding: 2px 6px;
    border-radius: 4px;
    text-transform: uppercase;
    font-weight: bold;
    background: rgba(52, 152, 219, 0.2);
    color: #3498db;
    border: 1px solid rgba(52, 152, 219, 0.3);
  }
  .profile-desc {
    margin: 4px 0 0 0;
    font-size: 0.85rem;
    color: #9ba3af;
  }
  .inline-input {
    background: transparent;
    border: 1px solid transparent;
    color: #e8edf2;
    padding: 6px 10px;
    border-radius: 4px;
    width: 100%;
    font-size: 0.9rem;
    transition: all 0.2s ease;
  }
  .inline-input:hover {
    border-color: #444c56;
    background: #0d1117;
  }
  .inline-input:focus {
    border-color: #58a6ff;
    background: #0d1117;
    outline: none;
    box-shadow: 0 0 0 2px rgba(88, 166, 255, 0.15);
  }
  .inline-select {
    background: transparent;
    border: 1px solid transparent;
    color: #e8edf2;
    padding: 6px 10px;
    border-radius: 4px;
    width: 100%;
    font-size: 0.9rem;
    cursor: pointer;
    transition: all 0.2s ease;
  }
  .inline-select:hover {
    border-color: #444c56;
    background: #0d1117;
  }
  .inline-select:focus {
    border-color: #58a6ff;
    background: #0d1117;
    outline: none;
  }
  .inline-input.new-input, .inline-select.new-input {
    border: 1px dashed #444c56;
    background: rgba(13, 17, 23, 0.5);
  }
  .inline-input.new-input:hover, .inline-select.new-input:hover {
    border-style: solid;
    border-color: #58a6ff;
    background: #0d1117;
  }
  .ticker-main-row:hover {
    background: rgba(255, 255, 255, 0.02);
  }
  .ticker-name-label {
    font-weight: 600;
    color: #f39c12;
    padding-left: 8px;
  }
  .nested-row {
    background: #111315;
  }
  .nested-table-container {
    padding: 16px 24px;
    border-left: 3px solid #3498db;
    background: rgba(52, 152, 219, 0.02);
    border-radius: 0 8px 8px 0;
  }
  .nested-table-container h4 {
    margin: 0 0 12px 0;
    font-size: 0.9rem;
    color: #9ba3af;
  }
  .highlight-text {
    color: #f39c12;
    font-weight: bold;
  }
  .nested-data-table {
    width: 100%;
    border-collapse: collapse;
    margin-top: 8px;
  }
  .nested-data-table th {
    text-align: left;
    padding: 8px 12px;
    font-size: 0.75rem;
    color: #8b949e;
    border-bottom: 1px solid #30363d;
    text-transform: uppercase;
  }
  .nested-tr {
    border-bottom: 1px solid #21262d;
  }
  .nested-tr:hover {
    background: rgba(255, 255, 255, 0.01);
  }
  .nested-tr td {
    padding: 6px 12px;
  }
  .add-sub-tr td {
    padding-top: 12px;
  }
  .add-ticker-tr {
    background: rgba(46, 204, 113, 0.03);
    border-top: 2px solid #21262d;
  }
  .add-ticker-tr td {
    padding: 16px 12px;
  }
  .btn-primary.btn-xs {
    padding: 4px 8px;
    font-size: 0.75rem;
    border-radius: 4px;
  }
  .btn-primary.btn-sm {
    padding: 6px 12px;
    font-size: 0.85rem;
    border-radius: 4px;
  }
</style>
