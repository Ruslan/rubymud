import { LatestRequest } from '../map/latestRequest';
import { formToSpec, type MapperProfileForm } from './mapperProfileForm';
import type { MapperReplayResult, MapperSpec } from './types';

export type MapperReplayToken = {
  generation: number;
  snapshot: string;
  name: string;
  spec: MapperSpec;
  sessionID: number;
  limit: number;
};

export type MapperReplayState = {
  busy: boolean;
  result: MapperReplayResult | null;
  error: string;
};

const emptyReplayState = (): MapperReplayState => ({ busy: false, result: null, error: '' });

function replaySnapshot(form: MapperProfileForm, sessionID: number, limit: number) {
  const name = form.name.trim() || 'draft';
  const spec = formToSpec(form);
  const numericLimit = Number(limit);
  return { snapshot: JSON.stringify({ name, spec, sessionID, limit: numericLimit }), name, spec, numericLimit };
}

// A replay result belongs to the exact draft and replay controls submitted to
// the server. Keep both a generation and a structural snapshot: form fields are
// bound directly in Svelte, so a user can change one while a request is pending.
export class MapperReplayRequest {
  private readonly latest = new LatestRequest();
  private replayState = emptyReplayState();

  state(): MapperReplayState {
    return this.replayState;
  }

  // Form transitions (edit/copy/clear and any changed draft control) must drop
  // completed samples as well as invalidate pending work. A stale request's
  // finally may run afterwards; finish() leaves this cleared state intact.
  clear(): MapperReplayState {
    this.latest.begin();
    this.replayState = emptyReplayState();
    return this.replayState;
  }

  begin(form: MapperProfileForm, sessionID: number, limit: number): MapperReplayToken {
    const { snapshot, name, spec, numericLimit } = replaySnapshot(form, sessionID, limit);
    const token = {
      generation: this.latest.begin(),
      snapshot,
      name,
      spec,
      sessionID,
      limit: numericLimit,
    };
    this.replayState = { busy: true, result: null, error: '' };
    return token;
  }

  isLatest(token: MapperReplayToken): boolean {
    return this.latest.isCurrent(token.generation);
  }

  isCurrent(token: MapperReplayToken, form: MapperProfileForm, sessionID: number, limit: number): boolean {
    if (!this.isLatest(token)) return false;
    return token.snapshot === replaySnapshot(form, sessionID, limit).snapshot;
  }

  succeed(token: MapperReplayToken, form: MapperProfileForm, sessionID: number, limit: number, result: MapperReplayResult): MapperReplayState {
    if (this.isCurrent(token, form, sessionID, limit)) {
      this.replayState = { busy: false, result, error: '' };
    }
    return this.replayState;
  }

  fail(token: MapperReplayToken, form: MapperProfileForm, sessionID: number, limit: number, error: string): MapperReplayState {
    if (this.isCurrent(token, form, sessionID, limit)) {
      this.replayState = { busy: false, result: null, error };
    }
    return this.replayState;
  }

  finish(token: MapperReplayToken): MapperReplayState {
    if (this.isLatest(token)) {
      this.replayState = { ...this.replayState, busy: false };
    }
    return this.replayState;
  }
}
