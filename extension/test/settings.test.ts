import assert from 'node:assert/strict';
import { test } from 'node:test';

import { DEFAULT_BASE_URL } from '../src/agent.ts';
import { baseUrlProblem, load, save, type Storage } from '../src/settings.ts';

// Where the control key is sent.
//
// The base URL is the address every /mask and /unmask call carries the key to, so a
// pasted host is a host that receives the secret. And the manifest lets the worker
// reach exactly one — http://127.0.0.1 — so any other reads as "the agent is not
// running" at somebody whose agent is.

/** memory is chrome.storage.local reduced to a map. */
function memory(): Storage {
  const items: Record<string, unknown> = {};
  return {
    async get(key) {
      return { [key]: items[key] };
    },
    async set(next) {
      Object.assign(items, next);
    },
  };
}

const KEY = 'a'.repeat(64);

test('an address on the host the manifest names is kept, less its trailing slash', async () => {
  const storage = memory();
  await save(storage, { baseUrl: 'http://127.0.0.1:9800/', key: KEY });
  assert.equal((await load(storage)).baseUrl, 'http://127.0.0.1:9800');
});

test('an address anywhere else falls back to the default rather than receiving the key', async () => {
  for (const baseUrl of [
    'http://evil.example:9787',
    'https://127.0.0.1:9787',
    'http://localhost:9787',
    'http://[::1]:9787',
    'http://127.0.0.2:9787',
    'not an address',
  ]) {
    const storage = memory();
    await save(storage, { baseUrl, key: KEY });
    assert.equal((await load(storage)).baseUrl, DEFAULT_BASE_URL, `${baseUrl} was kept`);
    assert.notEqual(baseUrlProblem(baseUrl), null, `${baseUrl} was not refused with a reason`);
  }
});

test('a stored address from before the rule is refused on the way in too', async () => {
  const storage = memory();
  await storage.set({ neverseen: { baseUrl: 'http://evil.example', key: KEY } });
  assert.equal((await load(storage)).baseUrl, DEFAULT_BASE_URL);
});

test('an empty address is not a problem: it means the default', () => {
  assert.equal(baseUrlProblem(''), null);
  assert.equal(baseUrlProblem('  '), null);
  assert.equal(baseUrlProblem(DEFAULT_BASE_URL), null);
});
