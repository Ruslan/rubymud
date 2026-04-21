<script lang="ts">
  import { onMount } from 'svelte';

  interface Variable {
    key: string;
    value: string;
  }

  let currentTab = 'variables';
  let variables: Variable[] = [];
  let newVarKey = '';
  let newVarValue = '';
  let loading = true;

  const tabs = [
    { id: 'variables', label: 'Variables' },
    { id: 'aliases', label: 'Aliases' },
    { id: 'triggers', label: 'Triggers' },
    { id: 'highlights', label: 'Highlights' },
    { id: 'sessions', label: 'Sessions' },
  ];

  async function fetchVariables() {
    loading = true;
    try {
      const res = await fetch('/api/variables');
      const data = await res.json();
      // Backend already returns an array of {key, value} objects
      variables = data || [];
    } catch (e) {
      console.error('Failed to fetch variables', e);
    } finally {
      loading = false;
    }
  }

  async function saveVariable() {
    if (!newVarKey.trim()) return;
    try {
      await fetch('/api/variables', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key: newVarKey, value: newVarValue })
      });
      newVarKey = '';
      newVarValue = '';
      await fetchVariables();
    } catch (e) {
      console.error('Failed to save variable', e);
    }
  }

  async function deleteVariable(key: string) {
    if (!confirm(`Delete variable "${key}"?`)) return;
    try {
      await fetch(`/api/variables/${key}`, { method: 'DELETE' });
      await fetchVariables();
    } catch (e) {
      console.error('Failed to delete variable', e);
    }
  }

  function editVariable(v: Variable) {
    newVarKey = v.key;
    newVarValue = v.value;
  }

  onMount(() => {
    fetchVariables();
  });
</script>

<main class="settings-layout">
  <nav class="sidebar">
    <div class="sidebar-header">
      <h1>Settings</h1>
    </div>
    <ul class="nav-links">
      {#each tabs as tab}
        <li class:active={currentTab === tab.id}>
          <button on:click={() => currentTab = tab.id}>{tab.label}</button>
        </li>
      {/each}
    </ul>
  </nav>

  <section class="content">
    {#if currentTab === 'variables'}
      <header class="content-header">
        <h2>Variables</h2>
        <p class="description">Global state variables used in triggers and commands.</p>
      </header>

      <div class="editor-box">
        <div class="form-row">
          <input type="text" bind:value={newVarKey} placeholder="Key (e.g. target)" />
          <input type="text" bind:value={newVarValue} placeholder="Value" />
          <button class="btn-primary" on:click={saveVariable}>Save</button>
        </div>
      </div>

      {#if loading}
        <div class="status">Loading...</div>
      {:else}
        <table class="data-table">
          <thead>
            <tr>
              <th>Key</th>
              <th>Value</th>
              <th style="width: 140px">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each variables as v}
              <tr>
                <td class="key-cell">{v.key}</td>
                <td class="value-cell">{v.value}</td>
                <td class="actions-cell">
                  <button class="btn-link" on:click={() => editVariable(v)}>Edit</button>
                  <button class="btn-link btn-danger" on:click={() => deleteVariable(v.key)}>Delete</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    {:else}
      <h2>{tabs.find(t => t.id === currentTab)?.label}</h2>
      <p class="status">Implementation coming soon...</p>
    {/if}
  </section>
</main>

<style>
  :global(body) {
    margin: 0;
    padding: 0;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    background: #111315;
    color: #e8edf2;
  }

  .settings-layout {
    display: grid;
    grid-template-columns: 240px 1fr;
    height: 100vh;
  }

  .sidebar {
    background: #1a1d21;
    border-right: 1px solid #2d333b;
    display: flex;
    flex-direction: column;
  }

  .sidebar-header {
    padding: 24px 20px;
    border-bottom: 1px solid #2d333b;
  }

  .sidebar-header h1 {
    margin: 0;
    font-size: 1.1rem;
    font-weight: 600;
    letter-spacing: 0.5px;
    color: #3498db;
  }

  .nav-links {
    list-style: none;
    padding: 10px 0;
    margin: 0;
  }

  .nav-links li button {
    width: 100%;
    padding: 12px 20px;
    text-align: left;
    background: none;
    border: none;
    color: #9ba3af;
    cursor: pointer;
    font-size: 0.95rem;
    transition: all 0.2s;
  }

  .nav-links li button:hover {
    background: #24292f;
    color: #e8edf2;
  }

  .nav-links li.active button {
    background: #24292f;
    color: #3498db;
    border-left: 3px solid #3498db;
    padding-left: 17px;
  }

  .content {
    padding: 40px;
    overflow-y: auto;
  }

  .content-header h2 {
    margin: 0 0 8px 0;
    font-size: 1.8rem;
  }

  .description {
    color: #9ba3af;
    margin-bottom: 32px;
  }

  .editor-box {
    background: #1a1d21;
    padding: 20px;
    border-radius: 8px;
    border: 1px solid #2d333b;
    margin-bottom: 32px;
  }

  .form-row {
    display: flex;
    gap: 12px;
  }

  input {
    background: #0d1117;
    border: 1px solid #30363d;
    color: #e8edf2;
    padding: 8px 12px;
    border-radius: 6px;
    font-size: 0.9rem;
    flex: 1;
  }

  input:focus {
    outline: none;
    border-color: #3498db;
    box-shadow: 0 0 0 2px rgba(52, 152, 219, 0.2);
  }

  .btn-primary {
    background: #3498db;
    color: white;
    border: none;
    padding: 8px 20px;
    border-radius: 6px;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.2s;
  }

  .btn-primary:hover {
    background: #2980b9;
  }

  .data-table {
    width: 100%;
    border-collapse: collapse;
    margin-top: 20px;
  }

  .data-table th {
    text-align: left;
    padding: 12px;
    border-bottom: 2px solid #30363d;
    color: #9ba3af;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .data-table td {
    padding: 12px;
    border-bottom: 1px solid #21262d;
    font-size: 0.9rem;
  }

  .key-cell {
    font-family: monospace;
    color: #f39c12;
    font-weight: 600;
  }

  .value-cell {
    color: #e8edf2;
  }

  .btn-link {
    background: none;
    border: none;
    color: #3498db;
    cursor: pointer;
    padding: 0;
    font-size: 0.9rem;
  }

  .btn-link:hover {
    text-decoration: underline;
  }

  .btn-danger {
    color: #e74c3c;
    margin-left: 12px;
  }

  .btn-danger:hover {
    color: #c0392b;
  }

  .status {
    color: #9ba3af;
    font-style: italic;
    margin-top: 20px;
  }
</style>
