import { describe, expect, it } from 'vitest';

import { emptyMapperProfileForm } from './mapperProfileForm';
import { MapperReplayRequest } from './mapperReplayRequest';

const replayResult = {
  ok: true,
  session_id: 7,
  scanned_lines: 10,
  candidate_lines: 1,
  matched_blocks: 1,
  samples: [{ log_id: 42, hint: 'Profile A room', desc: '', exits: 'N' }],
};

describe('MapperReplayRequest', () => {
  it('drops a replay response when its draft, session, or limit no longer matches', () => {
    const replay = new MapperReplayRequest();
    const form = emptyMapperProfileForm();
    form.name = 'draft';
    form.guard = '[ Exits:';
    form.pattern = '^\\[ Exits:\\s*(.*?)\\s*\\]$';

    const request = replay.begin(form, 7, 1000);
    expect(replay.isCurrent(request, form, 7, 1000)).toBe(true);

    form.guard = 'Changed:';
    expect(replay.isCurrent(request, form, 7, 1000)).toBe(false);
    form.guard = '[ Exits:';
    expect(replay.isCurrent(request, form, 8, 1000)).toBe(false);
    expect(replay.isCurrent(request, form, 7, 250)).toBe(false);
  });

  it('drops an older replay when a newer replay starts', () => {
    const replay = new MapperReplayRequest();
    const form = emptyMapperProfileForm();
    form.name = 'draft';
    form.guard = '[ Exits:';
    form.pattern = '^\\[ Exits:\\s*(.*?)\\s*\\]$';

    const older = replay.begin(form, 7, 1000);
    const newer = replay.begin(form, 7, 1000);

    expect(replay.isCurrent(older, form, 7, 1000)).toBe(false);
    expect(replay.isCurrent(newer, form, 7, 1000)).toBe(true);
  });

  it('clears completed state and invalidates pending requests on an editor transition', () => {
    const replay = new MapperReplayRequest();
    const form = emptyMapperProfileForm();
    form.name = 'profile-a';
    form.guard = '[ Exits:';
    form.pattern = '^\\[ Exits:\\s*(.*?)\\s*\\]$';

    const completed = replay.begin(form, 7, 1000);
    replay.succeed(completed, form, 7, 1000, replayResult);
    expect(replay.state()).toMatchObject({ busy: false, result: replayResult, error: '' });

    const failed = replay.begin(form, 7, 1000);
    replay.fail(failed, form, 7, 1000, 'Profile A replay failed');
    expect(replay.state()).toEqual({ busy: false, result: null, error: 'Profile A replay failed' });

    // Opening/copying/clearing another format uses this one reset operation.
    // It must hide Profile A's samples before Profile B is installed.
    const pending = replay.begin(form, 7, 1000);
    expect(replay.state().busy).toBe(true);
    replay.clear();
    expect(replay.isLatest(pending)).toBe(false);
    expect(replay.state()).toEqual({ busy: false, result: null, error: '' });

    // A pending request's finally runs after clear. It must not reintroduce
    // state or strand the UI in Running.
    replay.finish(pending);
    expect(replay.state()).toEqual({ busy: false, result: null, error: '' });
  });
});
