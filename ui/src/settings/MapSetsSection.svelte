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
    error = mapArchiveFileError(selectedFile);
    summary = null;
    importFailure = null;
  }

  async function upload() {
    const validationError = mapArchiveFileError(selectedFile);
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
          error: importFailureMessage(payload, `Upload failed (${response.status})`),
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
    if (!window.confirm(`Delete map set "${set.name}"? This also removes its rooms and generated room images.`)) return;
    deletingID = set.id;
    error = '';
    cleanupWarning = '';
    try {
      const response = await deleteMapSet(set.id);
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(importFailureMessage(payload, `Delete failed (${response.status})`));
      }
      if (typeof payload?.image_cleanup_error === 'string' && payload.image_cleanup_error) {
        cleanupWarning = `Map set was deleted, but image cleanup needs attention: ${payload.image_cleanup_error}`;
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
    <h2>Map Sets</h2>
    <p class="description">Import a ZIP archive as a new map set, then choose it for a session on the Sessions tab.</p>
  </header>

  <div class="editor-box map-set-upload">
    <div class="form-row map-set-upload-row">
      <input bind:this={fileInput} type="file" accept=".zip,application/zip" aria-label="Map ZIP archive" on:change={onFileChange} disabled={uploading || deletingID !== null} />
      <button class="btn-primary" on:click={upload} disabled={uploading || deletingID !== null || !selectedFile}>
        {uploading ? 'Importing...' : 'Import ZIP'}
      </button>
    </div>
    {#if error}<p class="form-error">{error}</p>{/if}
    {#if cleanupWarning}<p class="map-set-warning">{cleanupWarning}</p>{/if}
  </div>

  {#if summary}
    <section class="map-import-result" aria-live="polite">
      <strong>{summary.name}</strong>
      <span>{summary.zone_count} zones</span>
      <span>{summary.room_count} rooms</span>
      <span>{summary.seam_count} seams</span>
      {#if summary.unresolved.length}<p>Unresolved seams: {summary.unresolved.join('; ')}</p>{/if}
      {#if summary.conflicts.length}
        <p>Conflicts: {summary.conflicts.map(formatMapImportConflict).join('; ')}</p>
      {/if}
      {#if summary.conflicts_truncated}<p>Conflict report was truncated.</p>{/if}
    </section>
  {/if}

  {#if importFailure}
    <section class="map-import-result map-import-failure" aria-live="assertive">
      <p>{importFailure.error}</p>
      {#if importFailure.conflicts.length}
        <ul>
          {#each importFailure.conflicts as conflict}
            <li>{formatMapImportConflict(conflict)}</li>
          {/each}
        </ul>
      {/if}
      {#if importFailure.conflicts_truncated}<p>Conflict report was truncated.</p>{/if}
    </section>
  {/if}

  <table class="data-table map-sets-table">
    <thead><tr><th>Name</th><th>Map</th><th>Import</th><th>Actions</th></tr></thead>
    <tbody>
      {#each mapSets as set}
        <tr>
          <td class="key-cell">{set.name}</td>
          <td>{set.zone_count} zones, {set.room_count} rooms, {set.seam_count} seams</td>
          <td class="dim-cell">{set.source_archive || 'manual'}{set.imported_at ? `\n${new Date(set.imported_at).toLocaleString()}` : ''}</td>
          <td class="actions-cell"><button class="btn-link btn-danger" on:click={() => remove(set)} disabled={uploading || deletingID !== null}>{deletingID === set.id ? 'Deleting...' : 'Delete'}</button></td>
        </tr>
      {/each}
      {#if !loading && mapSets.length === 0}
        <tr><td colspan="4" class="dim-cell">No map sets imported yet.</td></tr>
      {/if}
      {#if loading}
        <tr><td colspan="4" class="dim-cell">Loading map sets...</td></tr>
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
