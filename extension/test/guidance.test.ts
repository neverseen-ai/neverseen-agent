import assert from 'node:assert/strict';
import { test } from 'node:test';

import { blockedMessage, diagnose, type Outcome } from '../src/guidance.ts';
import { type Health, levelOf } from '../src/protocol.ts';
import { load, looksLikeAKey, save, type Storage } from '../src/settings.ts';

// What the options page says, held apart from the page that says it.

const BASE = 'http://127.0.0.1:8787';

function healthy(over: Partial<Health> = {}): Health {
  return {
    status: 'ok',
    version: 'test',
    locales: ['fr'],
    substitution: 'token',
    secret_level: 'weak',
    masking: 'full',
    providers: ['anthropic'],
    available_locales: ['fr', 'gb', 'us'],
    groups: [],
    ...over,
  };
}

const ok = <T>(value: T): Outcome<T> => ({ ok: true, value });

test('a stopped agent is not reported as a broken extension', () => {
  const d = diagnose({ ok: false, reason: 'unreachable', message: 'refused' }, null, BASE);
  assert.equal(d.level, 'none');
  assert.match(d.headline, /not running/);
  assert.equal(d.command, 'neverseen proxy');
  assert.match(d.body, /unmasked/,
    'the consequence of a stopped agent is what somebody has to be told, not its status');
});

test('no key yet is its own state, not a refusal', () => {
  const d = diagnose(ok(healthy()), null, BASE);
  assert.equal(d.command, 'neverseen key');
  assert.match(d.headline, /connect/i);
});

test('a refused key says reconnect, not "the agent is down"', () => {
  // The two look identical if the only question asked needs a key, and telling them
  // apart is what keeps somebody from restarting a service that is running.
  const d = diagnose(ok(healthy()), { ok: false, reason: 'unauthorised', message: 'nope' }, BASE);
  assert.match(d.headline, /refused/);
  assert.equal(d.command, 'neverseen key');
  assert.match(d.body, /rotated|different agent/);
});

test('the agent’s own words survive a refusal it explained', () => {
  const d = diagnose(
    ok(healthy()),
    { ok: false, reason: 'refused', message: 'neverseen: no category named "EMIAL"' },
    BASE,
  );
  assert.match(d.body, /EMIAL/,
    'replacing the agent’s message throws away the only part somebody can act on');
});

test('an agent masking nothing is not green', () => {
  const d = diagnose(ok(healthy({ locales: [], masking: 'full' })), ok(null), BASE);
  assert.equal(d.level, 'none');
  assert.match(d.command ?? '', /--locales fr,gb,us/,
    'the command offers what this agent has, not what the page guessed');
});

test('a switched-off category degrades the indicator and is named', () => {
  // Level, not "is it masking". An agent with a category switched off *is* masking,
  // and a green light over that is a green light over the values not being replaced —
  // which is why the menu bar icon and the `neverseen status` exit code follow the
  // same three answers.
  const d = diagnose(
    ok(
      healthy({
        masking: 'partial',
        groups: [
          {
            code: 'personal',
            label: 'Personal',
            categories: [
              { code: 'EMAIL', label: 'Email address', off: true },
              { code: 'DOB', label: 'Date of birth' },
            ],
          },
        ],
      }),
    ),
    ok(null),
    BASE,
  );

  assert.equal(d.level, 'partial');
  assert.deepEqual(d.details, ['Email address'],
    'a count sends somebody looking; the name tells them whether it is the one they care about');
});

test('a fully masking agent is green and says what it is doing', () => {
  const d = diagnose(ok(healthy()), ok(null), BASE);
  assert.equal(d.level, 'full');
  assert.match(d.body, /fr/);
  assert.equal(d.command, undefined, 'a green state offered a command to fix it');
});

test('levelOf reads what the agent said, and falls back to what it carried', () => {
  assert.equal(levelOf(null), 'none');
  assert.equal(levelOf(healthy({ locales: [] })), 'none');
  assert.equal(levelOf(healthy({ masking: 'partial' })), 'partial');

  // A build whose /healthz predates the field: derived from the groups rather than
  // assumed, because assuming "full" over a switched-off category is the green light
  // this whole scale exists to avoid.
  assert.equal(
    levelOf(
      healthy({
        masking: '',
        groups: [{ code: 'personal', label: 'Personal', categories: [{ code: 'EMAIL', label: 'Email', off: true }] }],
      }),
    ),
    'partial',
  );
  assert.equal(levelOf(healthy({ masking: '' })), 'full');
});

test('every blocked reason names the command that fixes it', () => {
  assert.match(blockedMessage('unreachable', ''), /neverseen proxy/);
  assert.match(blockedMessage('unconfigured', ''), /neverseen key/);
  assert.match(blockedMessage('unauthorised', ''), /neverseen key/);
  assert.match(blockedMessage('refused', 'the agent said no'), /the agent said no/);

  for (const reason of ['unreachable', 'unconfigured', 'unauthorised', 'refused'] as const) {
    assert.match(blockedMessage(reason, 'x'), /not sent/,
      'a banner that does not say the message was not sent leaves somebody assuming it was');
  }
});

test('a pasted key is checked for shape before the agent is blamed for it', () => {
  assert.ok(looksLikeAKey('0'.repeat(64)));
  assert.ok(!looksLikeAKey('0'.repeat(63)), 'a truncated secret looks like protection and is not');
  assert.ok(!looksLikeAKey('$ neverseen key ' + '0'.repeat(64)), 'a pasted shell prompt');
  assert.ok(!looksLikeAKey('Z'.repeat(64)));
});

test('settings fall back to the documented default and survive a round trip', async () => {
  const backing = new Map<string, unknown>();
  const storage: Storage = {
    get: async (key) => ({ [key]: backing.get(key) }),
    set: async (items) => {
      for (const [k, v] of Object.entries(items)) backing.set(k, v);
    },
  };

  assert.deepEqual(await load(storage), { baseUrl: BASE, key: '' });

  await save(storage, { baseUrl: 'http://127.0.0.1:9999/', key: '  ' + 'a'.repeat(64) + '  ' });
  assert.deepEqual(await load(storage), {
    baseUrl: 'http://127.0.0.1:9999',
    key: 'a'.repeat(64),
  });
});
