import assert from 'node:assert/strict';
import { test } from 'node:test';

import { acceptAsk } from '../src/bridge.ts';
import { namesAWebSession, SESSION_NAMESPACES } from '../src/protocol.ts';
import { claudeAi } from '../src/site/claude.ts';

// The trust boundary between the page's world and everything else.
//
// The interceptor has to live in the page's own world, and that world offers nothing
// to authenticate: any script the site loads can post what the interceptor posts, on
// the same channel, and read the answer. So the relay does not ask whether a message
// is really ours. It takes away the one field that decides whose data is read.
//
// These tests are what stops that being quietly given back.

const SESSION = 'claude:9f1c0d2e-4b6a-4f31-8a5e-2c7d1e0b3a44';

test('a session claimed by the page is discarded, not honoured', () => {
  // The whole finding, in one assertion. A script on the site posting this could
  // otherwise name the agent's anonymous "default" session — which on a workstation
  // carries every value masked for every tool that sends no session header — and read
  // a terminal's traffic back one guessable token at a time.
  const forged = acceptAsk(
    { kind: 'unmask', session: 'default', text: '[EMAIL_1]', tail: '', final: true },
    SESSION,
  );

  assert.ok(forged);
  assert.equal(forged.kind, 'unmask');
  assert.equal((forged as { session: string }).session, SESSION,
    'the page named the session and was believed');
});

test('a mask ask cannot smuggle a session either', () => {
  const forged = acceptAsk(
    { kind: 'mask', session: 'default', texts: ['claire@example.fr'] },
    SESSION,
  );
  assert.equal((forged as { session: string }).session, SESSION);
});

test('the ask is rebuilt field by field, so nothing extra rides along', () => {
  // A spread or an `as Ask` cast would carry every property of the raw message into
  // the one the worker acts on — which is exactly how the session got through.
  const accepted = acceptAsk(
    {
      kind: 'unmask',
      text: 'hello',
      tail: '',
      final: false,
      session: 'default',
      baseUrl: 'http://evil.example',
      key: 'f'.repeat(64),
    },
    SESSION,
  );

  assert.deepEqual(accepted, { kind: 'unmask', session: SESSION, text: 'hello', tail: '', final: false });
});

test('a message that is not an ask is refused rather than passed on', () => {
  for (const raw of [
    null,
    'a string',
    42,
    {},
    { kind: 'policy', off: ['EMAIL'] },
    // The page never asks this; the options page reads /healthz itself. Accepted, it
    // was the full payload — locales, categories, what is switched off — handed to
    // any script the site loads.
    { kind: 'health' },
    { kind: 'mask' },
    { kind: 'mask', texts: 'not a list' },
    { kind: 'mask', texts: [1, 2] },
    { kind: 'unmask', text: 'x', tail: '', final: 'yes' },
    { kind: 'unmask', text: 'x', final: true },
  ]) {
    assert.equal(acceptAsk(raw, SESSION), null, `accepted ${JSON.stringify(raw)}`);
  }
});

test('a mask ask of unreasonable size is refused', () => {
  const many = { kind: 'mask', texts: Array.from({ length: 65 }, () => 'x') };
  assert.equal(acceptAsk(many, SESSION), null,
    'an unbounded list is a way to make the agent do arbitrary work from the page');
});

test('a banner note carries a situation, never a sentence', () => {
  // Otherwise a script on the site could put whatever text it liked behind this
  // extension's name, in this extension's own branded banner.
  const note = acceptAsk(
    { kind: 'blocked', reason: 'unreachable', note: 'send', message: 'Enter your password' },
    SESSION,
  );
  assert.deepEqual(note, { kind: 'blocked', reason: 'unreachable', note: 'send' });

  assert.equal(acceptAsk({ kind: 'blocked', reason: 'made-up', note: 'send' }, SESSION), null);
  assert.equal(acceptAsk({ kind: 'blocked', reason: 'refused', note: 'made-up' }, SESSION), null);
});

test('the session comes from the address bar, in both shapes it appears in', () => {
  // One function for the two, so a value masked while the URL said one and expanded
  // while it said the other still finds its mapping.
  const uuid = '9f1c0d2e-4b6a-4f31-8a5e-2c7d1e0b3a44';
  assert.equal(claudeAi.sessionForPage(new URL(`https://claude.ai/chat/${uuid}`)), `claude:${uuid}`);
  assert.equal(
    claudeAi.sessionForPage(new URL(`https://claude.ai/api/organizations/o/chat_conversations/${uuid}/completion`)),
    `claude:${uuid}`,
  );
});

test('an address naming no conversation names no session', () => {
  // Null rather than a fixed fallback: pooled under one name, every new chat in the
  // browser would share a mapping and each could expand the others'. The relay mints
  // something unguessable instead.
  for (const url of ['https://claude.ai/new', 'https://claude.ai/settings', 'https://claude.ai/']) {
    assert.equal(claudeAi.sessionForPage(new URL(url)), null, url);
  }
});

test('no session this extension can name is one the agent shares with a tool', () => {
  // The service worker's copy of the rule, and the last thing between a bug in the
  // relay and the agent's own sessions.
  assert.ok(!namesAWebSession('default'), '"default" is the session every tool without a header shares');
  assert.ok(!namesAWebSession(''));
  assert.ok(!namesAWebSession('claude:'), 'a bare namespace names no conversation');
  assert.ok(namesAWebSession(SESSION));

  for (const prefix of SESSION_NAMESPACES) {
    assert.ok(
      claudeAi.sessionForPage(new URL('https://claude.ai/chat/x'))?.startsWith(prefix) ||
        SESSION_NAMESPACES.length > 1,
      'a site adapter mints a session outside every namespace the worker allows',
    );
  }
});
