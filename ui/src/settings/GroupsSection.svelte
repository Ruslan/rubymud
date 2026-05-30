<script lang="ts">
  import './settings-section.css';
  import type { Profile, RuleGroupSummary } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let groups: RuleGroupSummary[] = [];
  export let toggleGroup: (groupName: string, enabled: boolean) => void | Promise<void>;
</script>

<section class="settings-section">
<header class="content-header"><h2>Groups</h2><p class="description">Bulk enable/disable rule groups for profile {currentProfile?.name}.</p></header>
{#if formError}<div class="form-error">{formError}</div>{/if}
<table class="data-table">
  <thead><tr><th>Group</th><th>Rules</th><th>Enabled</th><th>Disabled</th><th style="width: 180px">Actions</th></tr></thead>
  <tbody>
    {#each groups as group}
      <tr>
        <td class="value-cell">{group.group_name || 'default'}</td>
        <td class="dim-cell">{group.total_count} total</td>
        <td class="dim-cell">{group.enabled_count} on</td>
        <td class="dim-cell">{group.disabled_count} off</td>
        <td class="actions-cell">
          <button class="btn-link" on:click={() => toggleGroup(group.group_name, true)}>Enable</button>
          <button class="btn-link btn-danger" on:click={() => toggleGroup(group.group_name, false)}>Disable</button>
        </td>
      </tr>
    {/each}
  </tbody>
</table>
</section>
