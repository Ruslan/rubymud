import type { HistoryListResponse, LogEntry } from './types';

export type ProfileRuleDomain = 'aliases' | 'triggers' | 'subs' | 'highlights' | 'hotkeys' | 'declared_variables';

function apiToken(): string {
  // @ts-ignore injected by settings.html
  return window.API_TOKEN || '';
}

export function apiHeaders(json = false): Record<string, string> {
  const headers: Record<string, string> = { 'X-Session-Token': apiToken() };
  if (json) headers['Content-Type'] = 'application/json';
  return headers;
}

export function setAPIToken(token: string) {
  // @ts-ignore injected by settings.html
  window.API_TOKEN = token;
}

export async function getJSON<T = any>(url: string): Promise<T> {
  const res = await fetch(url, { headers: apiHeaders() });
  return await res.json();
}

function postJSON(url: string, body: any): Promise<Response> {
  return fetch(url, { method: 'POST', headers: apiHeaders(true), body: JSON.stringify(body) });
}

function putJSON(url: string, body: any): Promise<Response> {
  return fetch(url, { method: 'PUT', headers: apiHeaders(true), body: JSON.stringify(body) });
}

function postNoBody(url: string): Promise<Response> {
  return fetch(url, { method: 'POST', headers: apiHeaders() });
}

function deleteNoBody(url: string): Promise<Response> {
  return fetch(url, { method: 'DELETE', headers: apiHeaders() });
}

export const fetchSessions = () => getJSON('/api/sessions');
export const fetchProfiles = () => getJSON('/api/profiles');
export async function fetchColors() {
  const res = await fetch('/api/colors', { headers: apiHeaders() });
  if (!res.ok) return [];
  return await res.json();
}
export const fetchAppSettings = () => getJSON('/api/app/settings');
export const saveAppSettingsRequest = (settings: any) => putJSON('/api/app/settings', settings);
export const fetchProfileFiles = () => getJSON('/api/profiles/files');
export const fetchSessionProfiles = (sessionID: number) => getJSON(`/api/sessions/${sessionID}/profiles`);
export const fetchSessionVariables = (sessionID: number) => getJSON(`/api/sessions/${sessionID}/variables`);
export type FetchSessionHistoryOptions = { kind?: string; query?: string; beforeID?: number | null; limit?: number };
export function fetchSessionHistory(sessionID: number, options: FetchSessionHistoryOptions = {}) {
  const params = new URLSearchParams();
  params.set('limit', String(options.limit ?? 100));
  if (options.kind) params.set('kind', options.kind);
  if (options.query) params.set('q', options.query);
  if (options.beforeID) params.set('before_id', String(options.beforeID));
  return getJSON<HistoryListResponse>(`/api/sessions/${sessionID}/history?${params}`);
}
export const fetchProfileTimers = (profileID: number) => getJSON(`/api/profiles/${profileID}/timers`);

export function profileEndpoint(domain: string): string {
  return domain === 'declared_variables' ? 'variables' : domain;
}

export function fetchProfileDomain(profileID: number, domain: string) {
  return getJSON(`/api/profiles/${profileID}/${profileEndpoint(domain)}`);
}

export function saveItemRequest(domain: string, item: any, context: { selectedSessionID: number | null; selectedProfileID: number | null; variableKey: string; variableValue: string }) {
  const isUpdate = domain !== 'variables' && !!item.id;
  let url = `/api/${domain}`;

  if (domain === 'variables' && context.selectedSessionID) {
    url = `/api/sessions/${context.selectedSessionID}/variables`;
  } else if (['aliases', 'triggers', 'subs', 'highlights', 'hotkeys', 'declared_variables'].includes(domain) && context.selectedProfileID) {
    url = `/api/profiles/${context.selectedProfileID}/${profileEndpoint(domain)}`;
    if (isUpdate) url += `/${item.id}`;
  } else if (domain === 'sessions' && isUpdate) {
    url += `/${item.id}`;
  } else if (domain === 'profiles' && isUpdate) {
    url += `/${item.id}`;
  }

  const method = isUpdate ? 'PUT' : 'POST';
  const body = domain === 'variables' ? { key: context.variableKey, value: context.variableValue } : item;
  return fetch(url, { method, headers: apiHeaders(true), body: JSON.stringify(body) });
}

export function deleteItemRequest(domain: string, id: string | number, context: { selectedSessionID: number | null; selectedProfileID: number | null }) {
  let url = `/api/${domain}/${id}`;
  if (domain === 'variables' && context.selectedSessionID) {
    url = `/api/sessions/${context.selectedSessionID}/variables/${encodeURIComponent(String(id))}`;
  } else if (domain === 'history' && context.selectedSessionID) {
    url = `/api/sessions/${context.selectedSessionID}/history/${id}`;
  } else if (['aliases', 'triggers', 'subs', 'highlights', 'hotkeys', 'declared_variables'].includes(domain) && context.selectedProfileID) {
    url = `/api/profiles/${context.selectedProfileID}/${profileEndpoint(domain)}/${id}`;
  }
  return deleteNoBody(url);
}

export const saveRuntimeVariable = (sessionID: number, key: string, value: string) => postJSON(`/api/sessions/${sessionID}/variables`, { key, value });
export const saveProfileRule = (profileID: number, domain: string, id: number, draft: any) => putJSON(`/api/profiles/${profileID}/${profileEndpoint(domain)}/${id}`, draft);
export const saveProfile = (profileID: number, draft: any) => putJSON(`/api/profiles/${profileID}`, draft);
export const saveSession = (sessionID: number, draft: any) => putJSON(`/api/sessions/${sessionID}`, draft);

export function toggleProfileRule(profileID: number, domain: 'aliases' | 'triggers' | 'subs' | 'highlights', item: any, enabled: boolean) {
  return putJSON(`/api/profiles/${profileID}/${domain}/${item.id}`, { ...item, enabled });
}

export function toggleProfileGroup(profileID: number, groupName: string, enabled: boolean) {
  return postJSON(`/api/profiles/${profileID}/groups/toggle`, { group_name: groupName, enabled });
}

export function saveProfileTimerRequest(profileID: number, timer: any) {
  return postJSON(`/api/profiles/${profileID}/timers`, {
    profile_id: profileID,
    name: timer.name,
    icon: timer.icon || '',
    cycle_ms: parseInt(String(timer.cycle_ms), 10) || 1000,
    repeat_mode: timer.repeat_mode || 'repeating'
  });
}

export const deleteProfileTimerRequest = (profileID: number, name: string) => deleteNoBody(`/api/profiles/${profileID}/timers/${encodeURIComponent(name)}`);

export function saveProfileTimerSubscriptionRequest(profileID: number, timerName: string, sub: any) {
  return postJSON(`/api/profiles/${profileID}/timers/${encodeURIComponent(timerName)}/subscriptions`, {
    profile_id: profileID,
    timer_name: timerName,
    second: parseInt(String(sub.second), 10) || 0,
    sort_order: sub.sort_order || 0,
    command: sub.command || '',
    is_removal: !!sub.is_removal,
    is_bulk: !!sub.is_bulk
  });
}

export const deleteProfileTimerSubscriptionRequest = (profileID: number, timerName: string, sub: any) =>
  deleteNoBody(`/api/profiles/${profileID}/timers/${encodeURIComponent(timerName)}/subscriptions/${sub.second}/${sub.sort_order}`);

export const sessionActionRequest = (action: 'connect' | 'disconnect', id: number) => postNoBody(`/api/sessions/${id}/${action}`);
export const addProfileToSessionRequest = (sessionID: number, profileID: number, orderIndex: number) => postJSON(`/api/sessions/${sessionID}/profiles`, { profile_id: profileID, order_index: orderIndex });
export const removeProfileFromSessionRequest = (sessionID: number, profileID: number) => deleteNoBody(`/api/sessions/${sessionID}/profiles/${profileID}`);
export const reorderSessionProfilesRequest = (sessionID: number, payload: Array<{ profile_id: number; order_index: number }>) => putJSON(`/api/sessions/${sessionID}/profiles/reorder`, payload);

export const exportProfileRequest = (profileID: number) => postNoBody(`/api/profiles/${profileID}/export`);
export const exportAllProfilesRequest = () => postNoBody('/api/profiles/export/all');
export const importAllProfilesRequest = () => fetch('/api/profiles/import/all', { method: 'POST', headers: apiHeaders() });
export const importProfileFromFileRequest = (filename: string, sessionID: number | null) => postJSON('/api/profiles/import', { filename, session_id: sessionID });

export function moveProfileRulesRequest(profileID: number, domain: string, item: any, swapWith: any) {
  const endpoint = profileEndpoint(domain);
  return Promise.all([
    putJSON(`/api/profiles/${profileID}/${endpoint}/${item.id}`, item),
    putJSON(`/api/profiles/${profileID}/${endpoint}/${swapWith.id}`, swapWith),
  ]);
}

export const rotateAPITokenRequest = () => postNoBody('/api/app/settings/rotate-api-token');

export function fetchLogsRequest(sessionID: number, from: string, to: string, page: number, limit: number) {
  const params = new URLSearchParams({ from, to, page: String(page), limit: String(limit) });
  return getJSON(`/api/sessions/${sessionID}/logs?${params}`);
}

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return '';
  }
}

function tzQuery(): string {
  const tz = browserTimezone();
  return tz ? `&tz=${encodeURIComponent(tz)}` : '';
}

export function searchLogsRequest(sessionID: number, query: string, beforeID: number | null) {
  const url = `/api/sessions/${sessionID}/logs/search?q=${encodeURIComponent(query)}` + (beforeID ? `&before_id=${beforeID}` : '') + tzQuery();
  return getJSON(url);
}

export const fetchLogContext = (sessionID: number, entryID: number) => getJSON<LogEntry[]>(`/api/sessions/${sessionID}/logs/${entryID}/context?before=20&after=20${tzQuery()}`);
export const fetchMoreLogContext = (sessionID: number, entryID: number, direction: 'above' | 'below') => {
  const query = direction === 'above' ? 'before=50&after=0' : 'before=0&after=50';
  return getJSON<LogEntry[]>(`/api/sessions/${sessionID}/logs/${entryID}/context?${query}${tzQuery()}`);
};

export function buildLogDownloadURL(sessionID: number, from: string, to: string): string {
  const params = new URLSearchParams({ from, to, token: apiToken() });
  const tz = browserTimezone();
  if (tz) params.set('tz', tz);
  return `/api/sessions/${sessionID}/logs/download?${params}`;
}

export interface LogExportHTMLOptions {
  from: string;
  to: string;
  commands: boolean;
  theme: string;
  title?: string;
  buffer?: string;
}

// buildLogExportHTMLURL builds the server-side streaming colored-HTML export URL
// (mirrors buildLogDownloadURL). The server streams a self-contained .html file.
export function buildLogExportHTMLURL(sessionID: number, opts: LogExportHTMLOptions): string {
  const params = new URLSearchParams({ token: apiToken() });
  if (opts.from) params.set('from', opts.from);
  if (opts.to) params.set('to', opts.to);
  params.set('commands', opts.commands ? '1' : '0');
  if (opts.theme) params.set('theme', opts.theme);
  if (opts.title) params.set('title', opts.title);
  if (opts.buffer) params.set('buffer', opts.buffer);
  const tz = browserTimezone();
  if (tz) params.set('tz', tz);
  return `/api/sessions/${sessionID}/logs/export-html?${params}`;
}
