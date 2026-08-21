<script lang="ts">
  import './settings-section.css';
  import { mapperProfileLabel, mapperProvenanceNote, maxMapperExitsAlternatives } from './mapperProfileForm';
  import type { MapperProfileForm } from './mapperProfileForm';
  import { mapperProfileEditable, mapperProfileRef } from './types';
  import type { MapperProfile, MapperReplayResult, Session } from './types';
  import { readBootLocale, type Locale } from '../i18n';
  import { createSettingsMapperI18n } from './mapperI18n';

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
  export let locale: Locale = readBootLocale();
  $: i18n = createSettingsMapperI18n(locale);

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
  const scanPrefixFields = [
    ['N', 'scan_prefix_n'], ['E', 'scan_prefix_e'], ['S', 'scan_prefix_s'],
    ['W', 'scan_prefix_w'], ['U', 'scan_prefix_u'], ['D', 'scan_prefix_d'],
  ] as const;

  function setScanPrefix(field: typeof scanPrefixFields[number][1], value: string) {
    mapperForm[field] = value;
    mapperForm = mapperForm;
  }

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
    <h2>{i18n.t('roomFormatsTitle')}</h2>
    <p class="description">
      {i18n.t('formatsDescriptionA')} <code>rmud</code> {i18n.t('formatsDescriptionB')}
    </p>
    <p class="description">
      {i18n.t('formatsCustomBefore')} <em>{i18n.t('formatsCustomDetector')}</em>{i18n.t('formatsCustomAfter')}
    </p>
  </header>

  <div class="editor-box">
    {#if mapperProfileError}
      <div class="form-error">{mapperProfileError}</div>
    {:else if mapperProfileValidated}
      <div class="validated-ok">{i18n.t('formatValid')}</div>
    {/if}

    <div class="form-grid">
      <div class="form-row">
        <input
          type="text"
          bind:value={mapperForm.name}
          on:blur={validateMapperProfileDraft}
          placeholder={i18n.t('formatNamePlaceholder')}
          aria-label={i18n.t('formatNameAria')}
        />
        <span class="editing-indicator">
          {editingMapperProfileID === null ? i18n.t('newFormat') : i18n.t('editingNumber', { id: editingMapperProfileID })}
        </span>
      </div>

      <h4 class="group-title">{i18n.t('exits')}</h4>
      <div class="form-row">
        <label class="field">
          {i18n.t('guardLabel')}
          <input
            type="text"
            bind:value={mapperForm.guard}
            on:blur={validateMapperProfileDraft}
            placeholder="[ Exits:"
            aria-label={i18n.t('exitsGuardAria')}
          />
        </label>
        <label class="field">
          {i18n.t('noneToken')}
          <input
            type="text"
            bind:value={mapperForm.none_token}
            on:blur={validateMapperProfileDraft}
            placeholder="none"
            aria-label={i18n.t('exitsNoneAria')}
          />
        </label>
      </div>
      <div class="form-row">
        <label class="field wide">
          {i18n.t('exitsPatternLabel')}
          <input
            type="text"
            bind:value={mapperForm.pattern}
            on:blur={validateMapperProfileDraft}
            placeholder="^\[ Exits:\s*(.*?)\s*\]$"
            aria-label={i18n.t('exitsPatternAria')}
          />
        </label>
      </div>
      {#each mapperForm.exits_alt as rule, index}
        <div class="form-row" data-testid="exits-alternative">
          <label class="field">
            {i18n.t('alternativeGuard')}
            <input
              type="text"
              bind:value={rule.guard}
              on:blur={validateMapperProfileDraft}
              aria-label={i18n.t('alternativeGuardAria', { index: index + 1 })}
            />
          </label>
          <label class="field wide">
            {i18n.t('alternativePattern')}
            <input
              type="text"
              bind:value={rule.pattern}
              on:blur={validateMapperProfileDraft}
              aria-label={i18n.t('alternativePatternAria', { index: index + 1 })}
            />
          </label>
          <button
            type="button"
            class="btn-secondary"
            title={i18n.t('removeAlternative')}
            aria-label={i18n.t('removeAlternativeAria', { index: index + 1 })}
            on:click={() => removeExitsAlternative(index)}
          >×</button>
        </div>
      {/each}
      <div class="form-row">
        <button
          type="button"
          class="btn-secondary"
          disabled={mapperForm.exits_alt.length >= maxMapperExitsAlternatives}
          title={i18n.t('maxAlternativesTitle', { count: maxMapperExitsAlternatives })}
          on:click={addExitsAlternative}
        >{i18n.t('addAlternative')}</button>
        {#if mapperForm.exits_alt.length >= maxMapperExitsAlternatives}
          <span class="field-note">{i18n.t('maxAlternativesReached', { count: maxMapperExitsAlternatives })}</span>
        {/if}
      </div>

      <h4 class="group-title">{i18n.t('block')}</h4>
      <div class="form-row options-row">
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={mapperForm.stop_blank} on:change={validateMapperProfileDraft} />
          {i18n.t('stopBlank')}
        </label>
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={mapperForm.stop_prompt} on:change={validateMapperProfileDraft} />
          {i18n.t('stopPrompt')}
        </label>
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={mapperForm.stop_max_lines} on:change={validateMapperProfileDraft} />
          {i18n.t('stopMax')}
        </label>
      </div>
      <div class="form-row">
        <label class="field">
          {i18n.t('maxLines')}
          <input
            type="number"
            min="1"
            bind:value={mapperForm.max_lines}
            on:blur={validateMapperProfileDraft}
            aria-label={i18n.t('blockMaxAria')}
          />
        </label>
        <label class="field wide">
          {i18n.t('promptPatternLabel')}
          <input
            type="text"
            bind:value={mapperForm.prompt}
            on:blur={validateMapperProfileDraft}
            placeholder={i18n.t('promptPlaceholder')}
            aria-label={i18n.t('promptAria')}
          />
        </label>
        <label class="field wide">
          {i18n.t('doorCommandLabel')}
          <input
            type="text"
            bind:value={mapperForm.door_open}
            on:blur={validateMapperProfileDraft}
            placeholder={i18n.t('doorPlaceholder')}
            aria-label={i18n.t('doorAria')}
          />
          <small
            >{i18n.t('doorHelpA')} <code>&#123;dir&#125;</code> {i18n.t('doorHelpB')}
            <code>&#123;dir_en&#125;</code> {i18n.t('doorHelpC')}
            <code>откр две &#123;dir&#125;</code> {i18n.t('doorHelpD')}
            <code>open door &#123;dir_en&#125;</code> {i18n.t('doorHelpE')}</small
          >
        </label>
      </div>

      <h4 class="group-title">{i18n.t('scanHeading')}</h4>
      <p class="section-note">{i18n.t('scanHelp')}</p>
      <div class="form-row scan-fields">
        <label class="field wide">
          {i18n.t('scanCommandLabel')}
          <input type="text" bind:value={mapperForm.scan_command} on:blur={validateMapperProfileDraft} placeholder={i18n.t('scanCommandPlaceholder')} aria-label={i18n.t('scanCommandAria')} />
        </label>
        {#each scanPrefixFields as [dir, field]}
          <label class="field">
            {i18n.t('scanPrefixLabel', { dir })}
            <input
              type="text"
              value={mapperForm[field]}
              on:input={(event) => setScanPrefix(field, (event.currentTarget as HTMLInputElement).value)}
              on:blur={validateMapperProfileDraft}
              aria-label={i18n.t('scanPrefixAria', { dir })}
            />
          </label>
        {/each}
      </div>

      <h4 class="group-title">{i18n.t('obscuredHeading')}</h4>
      <p class="section-note">{i18n.t('obscuredHelp')}</p>
      <div class="form-row">
        <label class="field wide">
          {i18n.t('obscuredLabel')}
          <textarea
            rows="3"
            bind:value={mapperForm.obscured_lines}
            on:blur={validateMapperProfileDraft}
            placeholder={i18n.t('obscuredPlaceholder')}
            aria-label={i18n.t('obscuredAria')}
          ></textarea>
        </label>
      </div>

      <h4 class="group-title">{i18n.t('strip')}</h4>
      <div class="form-row">
        <label class="field">
          {i18n.t('mode')}
          <select bind:value={mapperForm.strip_mode} on:change={validateMapperProfileDraft} aria-label={i18n.t('stripModeAria')}>
            <option value="greedy">{i18n.t('greedyOption')}</option>
            <option value="min_run">{i18n.t('minRunOption')}</option>
            <option value="fixed_width">{i18n.t('fixedWidthOption')}</option>
          </select>
        </label>
        <label class="field">
          {i18n.t('glyphs')}{#if mapperForm.strip_mode === 'fixed_width' && glyphsDriveHeader}
            <span class="field-note">{i18n.t('glyphsFixedNote')}</span>
          {/if}
          <input
            type="text"
            bind:value={mapperForm.glyphs}
            on:blur={validateMapperProfileDraft}
            aria-label={i18n.t('glyphsAria')}
          />
        </label>
        {#if mapperForm.strip_mode === 'min_run'}
          <label class="field narrow">
            {i18n.t('minRun')}
            <input
              type="number"
              min="1"
              bind:value={mapperForm.min_run}
              on:blur={validateMapperProfileDraft}
              aria-label={i18n.t('stripMinRunAria')}
            />
          </label>
        {/if}
        {#if mapperForm.strip_mode === 'fixed_width'}
          <label class="field narrow">
            {i18n.t('width')}
            <input
              type="number"
              min="1"
              bind:value={mapperForm.width}
              on:blur={validateMapperProfileDraft}
              aria-label={i18n.t('stripWidthAria')}
            />
          </label>
        {/if}
      </div>

      <h4 class="group-title">{i18n.t('header')}</h4>
      <div class="form-row">
        <label class="field">
          {i18n.t('rule')}
          <select bind:value={mapperForm.header_rule} on:change={validateMapperProfileDraft} aria-label={i18n.t('headerRuleAria')}>
            <option value="last_before_stripped">{i18n.t('lastBeforeOption')}</option>
            <option value="first">{i18n.t('firstOption')}</option>
            <option value="ansi_delimited">{i18n.t('ansiOption')}</option>
          </select>
        </label>
        {#if mapperForm.header_rule === 'last_before_stripped'}
          <label class="field narrow">
            {i18n.t('columnRun')}
            <input
              type="number"
              min="1"
              bind:value={mapperForm.column_run}
              on:blur={validateMapperProfileDraft}
              aria-label={i18n.t('headerColumnAria')}
            />
          </label>
        {:else if mapperForm.header_rule === 'ansi_delimited'}
          <label class="field">
            {i18n.t('startSGR')}
            <input
              type="text"
              bind:value={mapperForm.ansi_start}
              on:blur={validateMapperProfileDraft}
              placeholder="1;36"
              aria-label={i18n.t('headerSGRAria')}
            />
          </label>
        {/if}
      </div>

      <div class="form-row options-row">
        <button class="btn-primary" on:click={saveMapperProfile}>
          {editingMapperProfileID === null ? i18n.t('add') : i18n.t('save')}
        </button>
        <button class="btn-link" on:click={cancelMapperProfileEdit}>{i18n.t('clear')}</button>
      </div>

      <h4 class="group-title">{i18n.t('replay')}</h4>
      <div class="form-row replay-controls">
        <label class="field wide">
          {i18n.t('session')}
          <select bind:value={mapperReplaySessionID} on:change={replayMapperProfileChanged} aria-label={i18n.t('replaySessionAria')}>
            {#each sessions as session}
              <option value={session.id}>{session.name}</option>
            {/each}
          </select>
        </label>
        <label class="field narrow">
          {i18n.t('recentLines')}
          <input
            type="number"
            min="1"
            max="5000"
            bind:value={mapperReplayLimit}
            on:input={replayMapperProfileChanged}
            aria-label={i18n.t('replayLimitAria')}
          />
        </label>
        <button
          type="button"
          class="btn-secondary"
          disabled={mapperReplaySessionID === null || mapperReplayBusy}
          on:click={replayMapperProfileDraft}
        >{mapperReplayBusy ? i18n.t('running') : i18n.t('runReplay')}</button>
      </div>
      {#if mapperReplayError}
        <div class="form-error replay-status">{mapperReplayError}</div>
      {:else if mapperReplayResult}
        <div class="replay-status">
          {i18n.t('replaySummary', {
            rooms: i18n.plural('room', mapperReplayResult.matched_blocks),
            lines: i18n.plural('lineAfterFrom', mapperReplayResult.scanned_lines),
            candidates: i18n.plural('exitsCandidate', mapperReplayResult.candidate_lines),
          })}
        </div>
        {#if mapperReplayResult.samples.length > 0}
          <div class="replay-results" aria-label={i18n.t('replayMatchesAria')}>
            {#each mapperReplayResult.samples as sample}
              <div class="replay-result">
                <div class="key-cell">{sample.hint || i18n.t('noRoomTitle')}</div>
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
        <th>{i18n.t('name')}</th>
        <!-- The reference column. Warnings and the mapper_profile status quote a
             machine reference ("builtin:diku-en", "db:7"); without this column the
             user had no way to map that string back to a row in this table. -->
        <th style="width: 150px">{i18n.t('reference')}</th>
        <th>{i18n.t('exitsGuard')}</th>
        <th>{i18n.t('stripColumn')}</th>
        <th>{i18n.t('headerColumn')}</th>
        <th style="width: 200px">{i18n.t('actions')}</th>
      </tr>
    </thead>
    <tbody>
      {#each mapperProfiles as p}
        <tr>
          <td>
            <div class="key-cell">{mapperProfileLabel(p, i18n)}</div>
            {#if p.provenance}
              <div class="comment-text">{mapperProvenanceNote(p, i18n)}</div>
            {/if}
            {#if p.mud}
              <div class="comment-text">{i18n.t('forMud', { mud: p.mud })}</div>
            {/if}
            {#if p.description}
              <div class="comment-text">{p.description}</div>
            {/if}
            {#if p.go_detector}
              <!-- A Go detector cannot be turned into an editable format, and offering
                   a copy would produce an empty one rather than something that works. -->
              <div class="comment-text">
                {i18n.t('goDetectorReadOnly')}
              </div>
            {:else if p.builtin}
              <div class="comment-text">{i18n.t('builtinReadOnly')}</div>
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
              <button class="btn-link" on:click={() => copyMapperProfile(p)}>{i18n.t('makeCopy')}</button>
            {:else if editingMapperProfileID === p.id}
              <span class="editing-indicator">{i18n.t('editing')}</span>
            {:else}
              <button class="btn-link" on:click={() => startMapperProfileEdit(p)}>{i18n.t('edit')}</button>
              <button class="btn-link" on:click={() => copyMapperProfile(p)}>{i18n.t('copy')}</button>
              <button class="btn-link btn-danger" on:click={() => deleteMapperProfileRow(p.id)}>{i18n.t('delete')}</button>
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
  .section-note {
    margin: 2px 0 8px;
    color: #8b949e;
    font-size: 12px;
  }
  .scan-fields {
    align-items: end;
    flex-wrap: wrap;
  }
  .scan-fields .field {
    flex: 1 1 150px;
  }
  .scan-fields .field.wide {
    flex-basis: 100%;
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
