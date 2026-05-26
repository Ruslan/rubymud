<script lang="ts">
  import './settings-section.css';
  import type { Profile, RuleGroupSummary } from './types';

  export let currentProfile: Profile | undefined;
  export let formError = '';
  export let groups: RuleGroupSummary[] = [];
  export let titleCase: (value: string) => string;
  export let ruleCountForGroup: (domain: string, groupName: string) => number;
  export let toggleGroup: (domain: string, groupName: string, enabled: boolean) => void | Promise<void>;
</script>

<section class="settings-section">
<header class="content-header"><h2>Groups</h2><p class="description">Bulk enable/disable rule groups for profile {currentProfile?.name}.</p></header>
{#if formError}<div class="form-error">{formError}</div>{/if}
<table class="data-table">
  <thead><tr><th>Domain</th><th>Group</th><th>Rules</th><th>Status</th><th style="width: 180px">Actions</th></tr></thead>
  <tbody>
    {#each groups as group}
      <tr>
        <td class="key-cell">{titleCase(group.domain)}</td>
        <td class="value-cell">{group.group_name || 'default'}</td>
        <td class="dim-cell">{ruleCountForGroup(group.domain, group.group_name)} total</td>
        <td class="dim-cell">{group.enabled_count} on / {group.disabled_count} off</td>
        <td class="actions-cell">
          <button class="btn-link" on:click={() => toggleGroup(group.domain, group.group_name, true)}>Enable</button>
          <button class="btn-link btn-danger" on:click={() => toggleGroup(group.domain, group.group_name, false)}>Disable</button>
        </td>
      </tr>
    {/each}
  </tbody>
</table>
</section>
