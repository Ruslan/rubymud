<script lang="ts">
  import './settings-section.css';
  import { mapperProfileLabel, mapperProvenanceNote, maxMapperExitsAlternatives } from './mapperProfileForm';
  import type { MapperProfileForm } from './mapperProfileForm';
  import { mapperProfileEditable, mapperProfileRef } from './types';
  import type { MapperProfile, MapperReplayResult, Session } from './types';

  // A ROOM FORMAT describes how to parse a room block out of the MUD's output.
  // It has nothing to do with the alias/trigger Profiles tab — see the note in
  // types.ts. Dumb component: all state and all API calls live in SettingsApp.
  export let mapperProfiles: MapperProfile[] = [];
  export let sessions: Session[] = [];
  export let mapperForm: MapperProfileForm;
  export let editingMapperProfileID: number | null = null;
  export let mapperProfileError = '';
  export let mapperProfileValidated = false;
  export let mapperReplaySessionID: number | null = null;
  export let mapperReplayLimit = 1000;
  export let mapperReplayResult: MapperReplayResult | null = null;
  export let mapperReplayError = '';
  export let mapperReplayBusy = false;

  export let saveMapperProfile: () => void | Promise<void>;
  export let validateMapperProfileDraft: () => void | Promise<void>;
  export let startMapperProfileEdit: (profile: MapperProfile) => void;
  export let copyMapperProfile: (profile: MapperProfile) => void;
  export let cancelMapperProfileEdit: () => void;
  export let deleteMapperProfileRow: (id: number) => void | Promise<void>;
  export let replayMapperProfileDraft: () => void | Promise<void>;
  export let replayMapperProfileChanged: () => void;

  // The strip mode decides which LENGTH field matters (min_run vs width); hiding
  // the irrelevant one is what makes this a structured editor rather than a JSON
  // blob with extra steps.
  //
  // Glyphs are deliberately NOT hidden under fixed_width. They are not a
  // strip-only field: the last_before_stripped header rule finds the room name
  // through them whatever the strip mode, so hiding (and blanking) them there
  // silently turns every room name into the movement preamble above it.
  $: glyphsDriveHeader = mapperForm.header_rule === 'last_before_stripped';

  // Bindings mutate the draft directly, so this is the one place that notices
  // every field transition and invalidates replay samples from the previous
  // draft. The initial snapshot deliberately does not clear anything.
  let mapperFormSnapshot = JSON.stringify(mapperForm);
  $: {
    const nextMapperFormSnapshot = JSON.stringify(mapperForm);
    if (nextMapperFormSnapshot !== mapperFormSnapshot) {
      mapperFormSnapshot = nextMapperFormSnapshot;
      replayMapperProfileChanged();
    }
  }

  function addExitsAlternative() {
    if (mapperForm.exits_alt.length >= maxMapperExitsAlternatives) return;
    mapperForm.exits_alt = [...mapperForm.exits_alt, { guard: '', pattern: '' }];
    mapperForm = mapperForm;
    void validateMapperProfileDraft();
  }

  function removeExitsAlternative(index: number) {
    mapperForm.exits_alt = mapperForm.exits_alt.filter((_, i) => i !== index);
    mapperForm = mapperForm;
    void validateMapperProfileDraft();
  }
</script>

<section class="settings-section">
  <header class="content-header">
    <h2>Room Formats</h2>
    <p class="description">
      A room format tells the mapper how to recognize a room block in the MUD's output:
      the exits line, where the block starts and ends, how the left column (the ASCII
      minimap) is cut off, and which line is the room name. Each session picks one on
      the Sessions tab; the builtin <code>rmud</code> format is used when none is chosen.
    </p>
    <p class="description">
      Text rules and ANSI-delimited room names can be configured here. Formats that need
      a custom state machine can use a <em>contributed Go detector</em>: a small reviewed
      file compiled into rubymud. Such a detector is listed here read-only, because
      there is no editable spec behind it.
    </p>
  </header>

  <div class="editor-box">
    {#if mapperProfileError}
      <div class="form-error">{mapperProfileError}</div>
    {:else if mapperProfileValidated}
      <div class="validated-ok">Format is valid.</div>
    {/if}

    <div class="form-grid">
      <div class="form-row">
        <input
          type="text"
          bind:value={mapperForm.name}
          on:blur={validateMapperProfileDraft}
          placeholder="Format name (e.g. rmud-custom)"
          aria-label="Room format name"
        />
        <span class="editing-indicator">
          {editingMapperProfileID === null ? 'New format' : `Editing #${editingMapperProfileID}`}
        </span>
      </div>

      <h4 class="group-title">Exits</h4>
      <div class="form-row">
        <label class="field">
          Guard (literal prefix)
          <input
            type="text"
            bind:value={mapperForm.guard}
            on:blur={validateMapperProfileDraft}
            placeholder="[ Exits:"
            aria-label="Exits guard"
          />
        </label>
        <label class="field">
          None token
          <input
            type="text"
            bind:value={mapperForm.none_token}
            on:blur={validateMapperProfileDraft}
            placeholder="none"
            aria-label="Exits none token"
          />
        </label>
      </div>
      <div class="form-row">
        <label class="field wide">
          Exits pattern (regex, exactly one capture group)
          <input
            type="text"
            bind:value={mapperForm.pattern}
            on:blur={validateMapperProfileDraft}
            placeholder="^\[ Exits:\s*(.*?)\s*\]$"
            aria-label="Exits pattern"
          />
        </label>
      </div>
      {#each mapperForm.exits_alt as rule, index}
        <div class="form-row" data-testid="exits-alternative">
          <label class="field">
            Alternative guard
            <input
              type="text"
              bind:value={rule.guard}
              on:blur={validateMapperProfileDraft}
              aria-label={`Alternative exits guard ${index + 1}`}
            />
          </label>
          <label class="field wide">
            Alternative pattern
            <input
              type="text"
              bind:value={rule.pattern}
              on:blur={validateMapperProfileDraft}
              aria-label={`Alternative exits pattern ${index + 1}`}
            />
          </label>
          <button
            type="button"
            class="btn-secondary"
            title="Remove exits alternative"
            aria-label={`Remove exits alternative ${index + 1}`}
            on:click={() => removeExitsAlternative(index)}
          >×</button>
        </div>
      {/each}
      <div class="form-row">
        <button
          type="button"
          class="btn-secondary"
          disabled={mapperForm.exits_alt.length >= maxMapperExitsAlternatives}
          title={`Maximum ${maxMapperExitsAlternatives} alternative exits wordings`}
          on:click={addExitsAlternative}
        >+ Alternative exits wording</button>
        {#if mapperForm.exits_alt.length >= maxMapperExitsAlternatives}
          <span class="field-note">Maximum {maxMapperExitsAlternatives} alternative exits wordings reached.</span>
        {/if}
      </div>

      <h4 class="group-title">Block</h4>
      <div class="form-row options-row">
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={mapperForm.stop_blank} on:change={validateMapperProfileDraft} />
          Stop at a blank line
        </label>
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={mapperForm.stop_prompt} on:change={validateMapperProfileDraft} />
          Stop at the prompt
        </label>
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={mapperForm.stop_max_lines} on:change={validateMapperProfileDraft} />
          Stop at max lines
        </label>
      </div>
      <div class="form-row">
        <label class="field">
          Max lines
          <input
            type="number"
            min="1"
            bind:value={mapperForm.max_lines}
            on:blur={validateMapperProfileDraft}
            aria-label="Block max lines"
          />
        </label>
        <label class="field wide">
          Prompt pattern (regex; required when stopping at the prompt)
          <input
            type="text"
            bind:value={mapperForm.prompt}
            on:blur={validateMapperProfileDraft}
            placeholder="(empty — RMUD's boundary is the blank line)"
            aria-label="Prompt pattern"
          />
        </label>
        <label class="field wide">
          Door-opening command (sent by a computed route before a door hop)
          <input
            type="text"
            bind:value={mapperForm.door_open}
            on:blur={validateMapperProfileDraft}
            placeholder="(empty — routes send nothing and doors are opened by hand)"
            aria-label="Door-opening command"
          />
          <small
            >Use <code>&#123;dir&#125;</code> for the Russian direction word or
            <code>&#123;dir_en&#125;</code> for the letter (n/s/e/w/u/d) — e.g.
            <code>откр две &#123;dir&#125;</code> on RMUD,
            <code>open door &#123;dir_en&#125;</code> on an English MUD. This is a command
            sent to the SERVER, so it follows the MUD, not the interface language. A
            per-edge command set in the map's edge editor always wins over it.</small
          >
        </label>
      </div>

      <h4 class="group-title">Left-column strip</h4>
      <div class="form-row">
        <label class="field">
          Mode
          <select bind:value={mapperForm.strip_mode} on:change={validateMapperProfileDraft} aria-label="Strip mode">
            <option value="greedy">greedy — cut the whole leading glyph run</option>
            <option value="min_run">min_run — cut it only when long enough</option>
            <option value="fixed_width">fixed_width — cut at a fixed column</option>
          </select>
        </label>
        <label class="field">
          Glyphs{#if mapperForm.strip_mode === 'fixed_width' && glyphsDriveHeader}
            <span class="field-note"> — not used by the fixed_width cut, but the header rule finds the room name through them</span>
          {/if}
          <input
            type="text"
            bind:value={mapperForm.glyphs}
            on:blur={validateMapperProfileDraft}
            aria-label="Left column glyphs"
          />
        </label>
        {#if mapperForm.strip_mode === 'min_run'}
          <label class="field narrow">
            Min run
            <input
              type="number"
              min="1"
              bind:value={mapperForm.min_run}
              on:blur={validateMapperProfileDraft}
              aria-label="Strip min run"
            />
          </label>
        {/if}
        {#if mapperForm.strip_mode === 'fixed_width'}
          <label class="field narrow">
            Width
            <input
              type="number"
              min="1"
              bind:value={mapperForm.width}
              on:blur={validateMapperProfileDraft}
              aria-label="Strip width"
            />
          </label>
        {/if}
      </div>

      <h4 class="group-title">Header</h4>
      <div class="form-row">
        <label class="field">
          Rule
          <select bind:value={mapperForm.header_rule} on:change={validateMapperProfileDraft} aria-label="Header rule">
            <option value="last_before_stripped">last_before_stripped — last line above the column</option>
            <option value="first">first — the first non-blank line</option>
            <option value="ansi_delimited">ansi_delimited — text marked by an ANSI SGR</option>
          </select>
        </label>
        {#if mapperForm.header_rule === 'last_before_stripped'}
          <label class="field narrow">
            Column run
            <input
              type="number"
              min="1"
              bind:value={mapperForm.column_run}
              on:blur={validateMapperProfileDraft}
              aria-label="Header column run"
            />
          </label>
        {:else if mapperForm.header_rule === 'ansi_delimited'}
          <label class="field">
            Start SGR parameters
            <input
              type="text"
              bind:value={mapperForm.ansi_start}
              on:blur={validateMapperProfileDraft}
              placeholder="1;36"
              aria-label="Header ANSI start SGR parameters"
            />
          </label>
        {/if}
      </div>

      <div class="form-row options-row">
        <button class="btn-primary" on:click={saveMapperProfile}>
          {editingMapperProfileID === null ? 'Add' : 'Save'}
        </button>
        <button class="btn-link" on:click={cancelMapperProfileEdit}>Clear</button>
      </div>

      <h4 class="group-title">Replay</h4>
      <div class="form-row replay-controls">
        <label class="field wide">
          Session
          <select bind:value={mapperReplaySessionID} on:change={replayMapperProfileChanged} aria-label="Replay session">
            {#each sessions as session}
              <option value={session.id}>{session.name}</option>
            {/each}
          </select>
        </label>
        <label class="field narrow">
          Recent lines
          <input
            type="number"
            min="1"
            max="5000"
            bind:value={mapperReplayLimit}
            on:input={replayMapperProfileChanged}
            aria-label="Replay line limit"
          />
        </label>
        <button
          type="button"
          class="btn-secondary"
          disabled={mapperReplaySessionID === null || mapperReplayBusy}
          on:click={replayMapperProfileDraft}
        >{mapperReplayBusy ? 'Running…' : 'Run replay'}</button>
      </div>
      {#if mapperReplayError}
        <div class="form-error replay-status">{mapperReplayError}</div>
      {:else if mapperReplayResult}
        <div class="replay-status">
          {mapperReplayResult.matched_blocks} rooms from {mapperReplayResult.scanned_lines} lines;
          {mapperReplayResult.candidate_lines} exits candidates.
        </div>
        {#if mapperReplayResult.samples.length > 0}
          <div class="replay-results" aria-label="Replay matches">
            {#each mapperReplayResult.samples as sample}
              <div class="replay-result">
                <div class="key-cell">{sample.hint || '(no room title)'}</div>
                <div class="value-cell">{sample.exits}</div>
                {#if sample.desc}<div class="comment-text">{sample.desc}</div>{/if}
              </div>
            {/each}
          </div>
        {/if}
      {/if}
    </div>
  </div>

  <table class="data-table">
    <thead>
      <tr>
        <th>Name</th>
        <!-- The reference column. Warnings and the mapper_profile status quote a
             machine reference ("builtin:diku-en", "db:7"); without this column the
             user had no way to map that string back to a row in this table. -->
        <th style="width: 150px">Reference</th>
        <th>Exits guard</th>
        <th>Strip</th>
        <th>Header</th>
        <th style="width: 200px">Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each mapperProfiles as p}
        <tr>
          <td>
            <div class="key-cell">{mapperProfileLabel(p)}</div>
            {#if p.provenance}
              <div class="comment-text">{mapperProvenanceNote(p)}</div>
            {/if}
            {#if p.mud}
              <div class="comment-text">For: {p.mud}</div>
            {/if}
            {#if p.description}
              <div class="comment-text">{p.description}</div>
            {/if}
            {#if p.go_detector}
              <!-- A Go detector cannot be turned into an editable format, and offering
                   a copy would produce an empty one rather than something that works. -->
              <div class="comment-text">
                Go detector compiled into rubymud — read-only, and it cannot be copied
                into an editable format.
              </div>
            {:else if p.builtin}
              <div class="comment-text">read-only — copy it to make changes</div>
            {/if}
          </td>
          <td class="value-cell"><code>{mapperProfileRef(p)}</code></td>
          <td class="value-cell">{p.spec?.exits?.guard || '-'}</td>
          <td class="dim-cell">{p.spec?.strip?.mode || '-'}</td>
          <td class="dim-cell">{p.spec?.header?.rule || '-'}</td>
          <td class="actions-cell">
            {#if !mapperProfileEditable(p)}
              <span class="dim-cell">-</span>
            {:else if p.builtin}
              <button class="btn-link" on:click={() => copyMapperProfile(p)}>Make a copy</button>
            {:else if editingMapperProfileID === p.id}
              <span class="editing-indicator">Editing…</span>
            {:else}
              <button class="btn-link" on:click={() => startMapperProfileEdit(p)}>Edit</button>
              <button class="btn-link" on:click={() => copyMapperProfile(p)}>Copy</button>
              <button class="btn-link btn-danger" on:click={() => deleteMapperProfileRow(p.id)}>Delete</button>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</section>

<style>
  .field-note {
    color: #6e7681;
    font-style: italic;
  }
  .comment-text {
    font-size: 12px;
    color: #8b949e;
  }
  .validated-ok {
    color: #3fb950;
    margin-bottom: 8px;
    font-size: 13px;
  }
  .group-title {
    margin: 14px 0 2px;
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #8b949e;
  }
  .replay-controls {
    align-items: end;
    flex-wrap: wrap;
  }
  .replay-controls .btn-secondary {
    min-height: 36px;
  }
  .replay-status {
    margin: 0;
    color: #9ba3af;
    font-size: 13px;
  }
  .replay-results {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 1px;
    background: #30363d;
    border: 1px solid #30363d;
    max-height: 280px;
    overflow: auto;
  }
  .replay-result {
    min-width: 0;
    padding: 10px;
    background: #0d1117;
  }
  .replay-result > div {
    overflow-wrap: anywhere;
  }
  /* settings-section.css styles inputs but not selects or field wrappers. */
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 12px;
    color: #8b949e;
    flex: 1;
  }
  .field.wide {
    flex: 3;
  }
  .field.narrow {
    flex: 0 0 120px;
  }
  .field select {
    background: #0d1117;
    border: 1px solid #30363d;
    color: #e8edf2;
    padding: 8px;
    border-radius: 6px;
    width: 100%;
    box-sizing: border-box;
  }
</style>
