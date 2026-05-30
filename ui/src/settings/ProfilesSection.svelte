<script lang="ts">
  import './settings-section.css';
  import type { Profile } from './types';

  export let formError = '';
  export let profiles: Profile[] = [];
  export let profileEditor: Partial<Profile>;
  export let profileFiles: { filename: string }[] = [];
  export let editingProfileID: number | null = null;
  export let editingProfileDraft: Partial<Profile>;
  export let inlineProfileError = '';

  export let addProfile: () => void | Promise<void>;
  export let exportAllProfiles: () => void | Promise<void>;
  export let importAllProfiles: () => void | Promise<void>;
  export let exportProfile: (id: number) => void | Promise<void>;
  export let importProfileFromFile: (filename: string) => void | Promise<void>;
  export let startInlineProfileEdit: (profile: Profile) => void;
  export let deleteProfile: (id: number) => void | Promise<void>;
  export let saveInlineProfileEdit: () => void | Promise<void>;
  export let cancelInlineProfileEdit: () => void;
</script>

<section class="settings-section">
<header class="content-header"><h2>Profiles</h2><p class="description">Manage settings profiles.</p></header>

<div class="editor-box" style="display: flex; gap: 12px; align-items: center; margin-bottom: 24px;">
  <button class="btn-primary" on:click={exportAllProfiles}>Export All to config/</button>
  <button class="btn-primary" style="background: #2ecc71" on:click={importAllProfiles}>Import All from config/</button>
</div>

<div class="editor-box">
  {#if formError}<div class="form-error">{formError}</div>{/if}
  <div class="form-row">
      <input type="text" bind:value={profileEditor.name} placeholder="Profile Name" required />
      <input type="text" bind:value={profileEditor.description} placeholder="Description" />
      <button class="btn-primary" on:click={addProfile}>
          Add
      </button>
  </div>
</div>
<table class="data-table">
  <thead><tr><th>Name</th><th>Description</th><th>Actions</th></tr></thead>
  <tbody>
    {#each profiles as p}
      <tr>
        <td class="key-cell">{p.name}</td>
        <td class="value-cell">{p.description || '-'}</td>
        <td class="actions-cell">
          {#if editingProfileID === p.id}
            <span class="editing-indicator">Editing…</span>
          {:else}
            <button class="btn-link" on:click={() => exportProfile(p.id)}>Export</button>
            <button class="btn-link" style="margin-left: 12px" on:click={() => startInlineProfileEdit(p)}>Edit</button>
            <button class="btn-link btn-danger" style="margin-left: 12px" on:click={() => deleteProfile(p.id)}>Delete</button>
          {/if}
        </td>
      </tr>
      {#if editingProfileID === p.id}
        <tr class="inline-edit-row">
          <td colspan="3">
            <div class="inline-edit-panel">
              <div class="inline-edit-grid">
                <label>Name <input type="text" bind:value={editingProfileDraft.name} aria-label="Profile name" /></label>
                <label class="inline-field-wide">Description <input type="text" bind:value={editingProfileDraft.description} aria-label="Profile description" /></label>
              </div>
              <div class="inline-edit-actions">
                <button class="btn-primary" on:click={saveInlineProfileEdit}>Save</button>
                <button class="btn-link" on:click={cancelInlineProfileEdit}>Cancel</button>
              </div>
              {#if inlineProfileError}<div class="form-error inline-error">{inlineProfileError}</div>{/if}
            </div>
          </td>
        </tr>
      {/if}
    {/each}
  </tbody>
</table>

<h3 style="margin-top: 40px; border-bottom: 1px solid #2d333b; padding-bottom: 8px;">Files in config/</h3>
<table class="data-table" style="margin-bottom: 20px;">
  <thead><tr><th>Filename</th><th style="width: 120px">Actions</th></tr></thead>
  <tbody>
      {#each profileFiles as file}
          <tr>
              <td class="value-cell">{file.filename}</td>
              <td class="actions-cell">
                  <button class="btn-link" on:click={() => importProfileFromFile(file.filename)}>Import</button>
              </td>
          </tr>
      {/each}
      {#if profileFiles.length === 0}
          <tr><td colspan="2" class="dim-cell">No .tt files found in config/ directory.</td></tr>
      {/if}
  </tbody>
</table>
</section>
