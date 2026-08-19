<script lang="ts">
  import './settings-section.css';
  import {
    deleteMapSet,
    fetchMapSets,
    importMapSet,
  } from './api';
  import type {
    MapImportFailure,
    MapImportSummary,
    MapSet,
  } from './api';
  import { formatMapImportConflict, importFailureMessage, mapArchiveFileError, MapSetListRevision } from './mapSets';
  import { readBootLocale, type Locale } from '../i18n';
  import { createSettingsMapperI18n } from './mapperI18n';

  export let locale: Locale = readBootLocale();
  $: i18n = createSettingsMapperI18n(locale);

  let mapSets: MapSet[] = [];
  let selectedFile: File | null = null;
  let fileInput: HTMLInputElement;
  let loading = true;
  let uploading = false;
  let deletingID: number | null = null;
  let error = '';
  let cleanupWarning = '';
  let summary: MapImportSummary | null = null;
  let importFailure: MapImportFailure | null = null;
  const listRevision = new MapSetListRevision();

  async function load() {
    const request = listRevision.begin();
    loading = true;
    try {
      const next = await fetchMapSets() || [];
      if (!listRevision.isCurrent(request)) return;
      mapSets = next;
      error = '';
    } catch (err) {
      if (!listRevision.isCurrent(request)) return;
      error = err instanceof Error ? err.message : String(err);
    } finally {
      if (listRevision.isCurrent(request)) loading = false;
    }
  }

  void load();

  function onFileChange(event: Event) {
    selectedFile = (event.currentTarget as HTMLInputElement).files?.[0] ?? null;
    error = mapArchiveFileError(selectedFile, i18n);
    summary = null;
    importFailure = null;
  }

  async function upload() {
    const validationError = mapArchiveFileError(selectedFile, i18n);
    if (validationError || !selectedFile) {
      error = validationError;
      return;
    }
    uploading = true;
    error = '';
    summary = null;
    importFailure = null;
    try {
      const response = await importMapSet(selectedFile);
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        importFailure = {
          error: importFailureMessage(payload, i18n.t('uploadFailed', { status: response.status })),
          conflicts: Array.isArray(payload?.conflicts) ? payload.conflicts : [],
          conflicts_truncated: payload?.conflicts_truncated === true,
        };
        return;
      }
      summary = payload as MapImportSummary;
      selectedFile = null;
      fileInput.value = '';
      await load();
      window.dispatchEvent(new CustomEvent('map-sets-changed'));
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      uploading = false;
    }
  }

  async function remove(set: MapSet) {
    if (!window.confirm(i18n.t('deleteMapSetConfirm', { name: set.name }))) return;
    deletingID = set.id;
    error = '';
    cleanupWarning = '';
    try {
      const response = await deleteMapSet(set.id);
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(importFailureMessage(payload, i18n.t('deleteFailed', { status: response.status })));
      }
      if (typeof payload?.image_cleanup_error === 'string' && payload.image_cleanup_error) {
        cleanupWarning = i18n.t('cleanupWarning', { error: payload.image_cleanup_error });
      }
      mapSets = mapSets.filter((item) => item.id !== set.id);
      window.dispatchEvent(new CustomEvent('map-sets-changed'));
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      deletingID = null;
    }
  }
</script>

<section class="settings-section map-sets-section">
  <header class="content-header">
    <h2>{i18n.t('mapSetsTitle')}</h2>
    <p class="description">{i18n.t('mapSetsDescription')}</p>
  </header>

  <div class="editor-box map-set-upload">
    <div class="form-row map-set-upload-row">
      <input bind:this={fileInput} type="file" accept=".zip,application/zip" aria-label={i18n.t('mapZipAria')} on:change={onFileChange} disabled={uploading || deletingID !== null} />
      <button class="btn-primary" on:click={upload} disabled={uploading || deletingID !== null || !selectedFile}>
        {uploading ? i18n.t('importing') : i18n.t('importZip')}
      </button>
    </div>
    {#if error}<p class="form-error">{error}</p>{/if}
    {#if cleanupWarning}<p class="map-set-warning">{cleanupWarning}</p>{/if}
  </div>

  {#if summary}
    <section class="map-import-result" aria-live="polite">
      <strong>{summary.name}</strong>
      <span>{i18n.plural('zone', summary.zone_count)}</span>
      <span>{i18n.plural('room', summary.room_count)}</span>
      <span>{i18n.plural('seam', summary.seam_count)}</span>
      {#if summary.unresolved.length}<p>{i18n.t('unresolvedSeams', { seams: summary.unresolved.join('; ') })}</p>{/if}
      {#if summary.conflicts.length}
        <p>{i18n.t('conflicts', { conflicts: summary.conflicts.map((conflict) => formatMapImportConflict(conflict, i18n)).join('; ') })}</p>
      {/if}
      {#if summary.conflicts_truncated}<p>{i18n.t('conflictTruncated')}</p>{/if}
    </section>
  {/if}

  {#if importFailure}
    <section class="map-import-result map-import-failure" aria-live="assertive">
      <p>{importFailure.error}</p>
      {#if importFailure.conflicts.length}
        <ul>
          {#each importFailure.conflicts as conflict}
            <li>{formatMapImportConflict(conflict, i18n)}</li>
          {/each}
        </ul>
      {/if}
      {#if importFailure.conflicts_truncated}<p>{i18n.t('conflictTruncated')}</p>{/if}
    </section>
  {/if}

  <table class="data-table map-sets-table">
    <thead><tr><th>{i18n.t('name')}</th><th>{i18n.t('map')}</th><th>{i18n.t('import')}</th><th>{i18n.t('actions')}</th></tr></thead>
    <tbody>
      {#each mapSets as set}
        <tr>
          <td class="key-cell">{set.name}</td>
          <td>{i18n.plural('zone', set.zone_count)}, {i18n.plural('room', set.room_count)}, {i18n.plural('seam', set.seam_count)}</td>
          <td class="dim-cell">{set.source_archive || i18n.t('manual')}{set.imported_at ? `\n${new Date(set.imported_at).toLocaleString(i18n.locale)}` : ''}</td>
          <td class="actions-cell"><button class="btn-link btn-danger" on:click={() => remove(set)} disabled={uploading || deletingID !== null}>{deletingID === set.id ? i18n.t('deleting') : i18n.t('delete')}</button></td>
        </tr>
      {/each}
      {#if !loading && mapSets.length === 0}
        <tr><td colspan="4" class="dim-cell">{i18n.t('noMapSets')}</td></tr>
      {/if}
      {#if loading}
        <tr><td colspan="4" class="dim-cell">{i18n.t('loadingMapSets')}</td></tr>
      {/if}
    </tbody>
  </table>
</section>

<style>
  .map-set-upload-row { flex-wrap: wrap; }
  .map-set-upload-row input { flex: 1 1 260px; }
  .map-import-result { border-top: 1px solid #30363d; border-bottom: 1px solid #30363d; padding: 12px 0; margin: 0 0 20px; display: flex; flex-wrap: wrap; gap: 8px 16px; color: #c9d1d9; }
  .map-import-result p { flex-basis: 100%; margin: 0; color: #9ba3af; }
  .map-import-result ul { flex-basis: 100%; margin: 0; padding-left: 20px; color: #ffb86c; }
  .map-import-failure { color: #ff7b72; }
  .map-set-warning { color: #ffb86c; margin: 12px 0 0; }
  .map-sets-table td:nth-child(3) { white-space: pre-line; }
  @media (max-width: 640px) {
    .map-sets-table th:nth-child(3), .map-sets-table td:nth-child(3) { display: none; }
  }
</style>
