import assert from 'node:assert/strict';
import { test } from 'node:test';

import { Blocked, install, wrapFetch, type Send } from '../src/intercept.ts';
import { type PageAsk, type Reply } from '../src/protocol.ts';
import { claudeAi } from '../src/site/claude.ts';

// The outbound half, driven without a browser.
//
// `location` is what the wrappers resolve a relative address against, and node has
// none. Set once here rather than passed through every signature: in the extension it
// is the page's own, which is the thing being described.
(globalThis as { location?: unknown }).location = new URL('https://claude.ai/chat/9f1c');

const SEND_URL =
  'https://claude.ai/api/organizations/org-1/chat_conversations/9f1c0d2e-4b6a-4f31-8a5e-2c7d1e0b3a44/completion';

/** relay is a stub service worker: it answers asks and records them. */
function relay(handlers: Partial<Record<PageAsk['kind'], (ask: PageAsk) => Reply<unknown>>>) {
  const asked: PageAsk[] = [];
  const send: Send = async (ask) => {
    asked.push(ask);
    const handler = handlers[ask.kind];
    return handler ? handler(ask) : { ok: true, result: null };
  };
  return { asked, send };
}

/** masking answers /mask by bracketing every address it is given, so a test can see
 * what left without running a detector. */
const masking = (ask: PageAsk): Reply<unknown> => {
  const texts = (ask as unknown as { texts: string[] }).texts;
  return {
    ok: true,
    result: {
      texts: texts.map((t) => t.replace(/[\w.]+@[\w.]+/g, '[EMAIL_1]')),
      masked: 1,
    },
  };
};

test('a send is masked before it leaves', async () => {
  let sent: Request | undefined;
  const original = (async (input: RequestInfo | URL) => {
    sent = input as Request;
    return new Response('{}', { headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;

  const { send } = relay({ mask: masking });
  await wrapFetch(original, claudeAi, send)(SEND_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ prompt: 'write to claire@example.fr', model: 'claude' }),
  });

  const body = JSON.parse(await sent!.text()) as { prompt: string; model: string };
  assert.equal(body.prompt, 'write to [EMAIL_1]', 'the address reached the site in clear');
  assert.equal(body.model, 'claude',
    'a field that is not typed text was rewritten; the site cannot route on it');
});

test('nothing leaving the page world names a session', async () => {
  // The security property, asserted where it can regress. This code runs in the
  // page's own world, so anything it says about whose mapping to read, a script the
  // site loads can say too — by posting the same message on the same channel. The
  // session is therefore not the page's to state: the relay decides it from its own
  // location, and a field reappearing here would hand the choice straight back.
  const original = (async () =>
    new Response('data: {"delta":{"text":"[EMAIL_1]"}}\n\n', {
      headers: { 'Content-Type': 'text/event-stream' },
    })) as typeof fetch;

  const { asked, send } = relay({
    mask: masking,
    unmask: () => ({ ok: true, result: { expanded: 'x', tail: '' } }),
  });

  await (
    await wrapFetch(original, claudeAi, send)(SEND_URL, {
      method: 'POST',
      body: JSON.stringify({ prompt: 'hello claire@example.fr' }),
    })
  ).text();

  assert.ok(asked.length >= 2, 'the round trip asked nothing, so this proves nothing');
  for (const ask of asked) {
    assert.ok(
      !Object.prototype.hasOwnProperty.call(ask, 'session'),
      `a ${ask.kind} ask carried a session out of the page's world`,
    );
  }
});

test('a request that is not a send is left completely alone', async () => {
  let calls = 0;
  const original = (async () => {
    calls++;
    return new Response('{}');
  }) as typeof fetch;
  const { asked, send } = relay({ mask: masking });
  const wrapped = wrapFetch(original, claudeAi, send);

  await wrapped('https://claude.ai/api/organizations/org-1/chat_conversations', { method: 'GET' });
  await wrapped('https://claude.ai/_next/static/chunk.js');
  await wrapped('https://example.com/anything', { method: 'POST', body: 'x' });

  assert.equal(calls, 3);
  assert.equal(asked.length, 0, "the extension asked the agent about somebody else's traffic");
});

test('the send is blocked when the agent cannot be reached', async () => {
  // Fail closed. This is the extension's mirror of the proxy's 415 on a body it
  // cannot read, and it is the rule somebody will be tempted to soften: a blocked
  // send looks like a broken site, and a forwarded one looks like nothing at all.
  let reached = false;
  const original = (async () => {
    reached = true;
    return new Response('{}');
  }) as typeof fetch;

  const { asked, send } = relay({
    mask: () => ({ ok: false, reason: 'unreachable', message: 'connection refused' }),
  });

  await assert.rejects(
    wrapFetch(original, claudeAi, send)(SEND_URL, {
      method: 'POST',
      body: JSON.stringify({ prompt: 'write to claire@example.fr' }),
    }),
    (err: unknown) => err instanceof Blocked && err.reason === 'unreachable',
  );

  assert.equal(reached, false, 'the message was forwarded in clear');
  assert.ok(
    asked.some((a) => a.kind === 'blocked'),
    'nothing told the person why their message did not send',
  );
});

test('a send whose body cannot be read is blocked, not forwarded', async () => {
  let reached = false;
  const original = (async () => {
    reached = true;
    return new Response('{}');
  }) as typeof fetch;
  const { send } = relay({ mask: masking });

  await assert.rejects(
    wrapFetch(original, claudeAi, send)(SEND_URL, {
      method: 'POST',
      body: new URLSearchParams({ prompt: 'claire@example.fr' }),
    }),
    (err: unknown) => err instanceof Blocked,
  );
  assert.equal(reached, false);
});

test('a send carrying no field this knows to mask is blocked, not forwarded', async () => {
  // The site's own path says it is a send, so whatever the body carries reaches the
  // model. A shape the adapter does not recognise is the site having moved its
  // prompt — forwarded, the extension masks nothing while looking installed.
  let reached = false;
  const original = (async () => {
    reached = true;
    return new Response('{}');
  }) as typeof fetch;
  const { asked, send } = relay({ mask: masking });

  await assert.rejects(
    wrapFetch(original, claudeAi, send)(SEND_URL, {
      method: 'POST',
      body: JSON.stringify({ message: 'write to claire@example.fr', model: 'claude' }),
    }),
    (err: unknown) => err instanceof Blocked && err.reason === 'refused',
  );
  assert.equal(reached, false, 'the message was forwarded in clear');
  assert.ok(asked.some((a) => a.kind === 'blocked'), 'nothing told the person why');
  assert.ok(!asked.some((a) => a.kind === 'mask'), 'the agent was asked to mask nothing');
});

test('a Request keeps its referrer through the rebuild', async () => {
  // A non-empty init resets the referrer and its policy on the copy, and the site's
  // server may check the referrer on a send.
  let sent: Request | undefined;
  const original = (async (input: RequestInfo | URL) => {
    sent = input as Request;
    return new Response('{}');
  }) as typeof fetch;
  const { send } = relay({ mask: masking });

  const request = new Request(SEND_URL, {
    method: 'POST',
    body: JSON.stringify({ prompt: 'hello' }),
    referrer: 'https://claude.ai/chat/9f1c',
    referrerPolicy: 'strict-origin-when-cross-origin',
  });
  await wrapFetch(original, claudeAi, send)(request);

  assert.equal(sent!.referrer, 'https://claude.ai/chat/9f1c');
  assert.equal(sent!.referrerPolicy, 'strict-origin-when-cross-origin');
});

test('a streamed answer is wrapped, and any other answer is not', async () => {
  const sse = (async () =>
    new Response('data: {"delta":{"text":"hi"}}\n\n', {
      headers: { 'Content-Type': 'text/event-stream' },
    })) as typeof fetch;
  const json = (async () =>
    new Response('{"ok":true}', {
      headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;

  const { asked, send } = relay({
    mask: masking,
    unmask: (ask) => {
      const chunk = ask as unknown as { tail: string; text: string };
      return { ok: true, result: { expanded: chunk.tail + chunk.text, tail: '' } };
    },
  });

  const body = JSON.stringify({ prompt: 'hello' });
  await (await wrapFetch(sse, claudeAi, send)(SEND_URL, { method: 'POST', body })).text();
  assert.ok(asked.some((a) => a.kind === 'unmask'), 'a streamed answer went to the page unexpanded');

  const before = asked.length;
  await (await wrapFetch(json, claudeAi, send)(SEND_URL, { method: 'POST', body })).text();
  assert.equal(
    asked.slice(before).filter((a) => a.kind === 'unmask').length,
    0,
    'a body that is not a stream was pushed through the streaming protocol',
  );
});

test('the answer keeps its status and its headers', async () => {
  // A stream handed back under a fabricated 200 hides an error the site knows how to
  // show, and the person is left with a page that spins.
  const original = (async () =>
    new Response('data: {"delta":{"text":"x"}}\n\n', {
      status: 429,
      statusText: 'Too Many Requests',
      headers: { 'Content-Type': 'text/event-stream', 'X-Ratelimit-Reset': '60' },
    })) as typeof fetch;

  const { send } = relay({
    mask: masking,
    unmask: () => ({ ok: true, result: { expanded: 'x', tail: '' } }),
  });

  const resp = await wrapFetch(original, claudeAi, send)(SEND_URL, {
    method: 'POST',
    body: JSON.stringify({ prompt: 'hello' }),
  });

  assert.equal(resp.status, 429);
  assert.equal(resp.headers.get('X-Ratelimit-Reset'), '60');
});

test('a transport that cannot be masked is refused rather than forwarded', async () => {
  // XMLHttpRequest.send and sendBeacon are synchronous and a WebSocket is open before
  // there is anything to look at, so none of the three can be masked. A site that
  // moved its chat onto one of them would otherwise go on working with this extension
  // installed and mask nothing at all — the one failure worse than a blocked send.
  const opened: string[] = [];
  const beaconed: string[] = [];
  const socketed: string[] = [];

  class FakeXHR {
    open(_method: string, url: string) {
      opened.push(url);
    }
    send() {
      opened.push('SENT');
    }
  }
  class FakeSocket {
    static OPEN = 1;
    constructor(url: string) {
      socketed.push(url);
    }
  }

  const target = {
    fetch: async () => new Response('{}'),
    XMLHttpRequest: FakeXHR,
    WebSocket: FakeSocket,
    navigator: {
      sendBeacon: (url: string) => {
        beaconed.push(url);
        return true;
      },
    },
  } as unknown as Window & typeof globalThis;

  const { asked, send } = relay({});
  install(target, claudeAi, send);

  const xhr = new target.XMLHttpRequest();
  xhr.open('POST', SEND_URL);
  assert.throws(() => xhr.send(), (err: unknown) => err instanceof Blocked);
  assert.ok(!opened.includes('SENT'), 'the message went out over XMLHttpRequest, unmasked');

  assert.equal(target.navigator.sendBeacon(SEND_URL, 'x'), false);
  assert.equal(beaconed.length, 0, 'the message went out over sendBeacon, unmasked');

  assert.throws(
    () => new target.WebSocket(SEND_URL.replace('https', 'wss')),
    (err: unknown) => err instanceof Blocked,
  );
  assert.equal(socketed.length, 0, 'the chat connection was opened, and nothing on it is masked');
  assert.equal(target.WebSocket.OPEN, 1,
    'the replacement lost the statics; a site comparing readyState against WebSocket.OPEN never sends');

  assert.equal(
    asked.filter((a) => a.kind === 'blocked').length,
    3,
    'a refusal that says nothing leaves somebody believing the site is broken',
  );

  // And traffic that is not the chat still goes through all three untouched.
  const other = new target.XMLHttpRequest();
  other.open('GET', 'https://claude.ai/_next/static/chunk.js');
  other.send();
  assert.ok(opened.includes('SENT'));

  // As does a read of the chat: a GET carries nothing typed, and the same GET over
  // fetch passes untouched.
  const read = new target.XMLHttpRequest();
  read.open('GET', SEND_URL);
  assert.doesNotThrow(() => read.send(), 'a read of the conversation was refused');
  assert.equal(target.navigator.sendBeacon('https://claude.ai/telemetry', 'x'), true);
  assert.equal(beaconed.length, 1);
});

const RETRY_URL =
  'https://claude.ai/api/organizations/org-1/chat_conversations/9f1c0d2e-4b6a-4f31-8a5e-2c7d1e0b3a44/retry_completion';

// Retry is a send by the site's own path — claudeAi.isSend matches it deliberately,
// so that no path reaches the model outside this wrapper — but it carries no prompt:
// it re-runs a turn the site already holds, from parent_message_uuid. Read through
// the empty-texts refusal that guards a moved prompt field, every Retry on the page
// was blocked outright and the request never left.
//
// Both halves, because either alone hides the failure the other would catch: a
// forwarded retry whose answer is not restored renders the stored turn's stand-ins
// to the person as [EMAIL_1].
test('a retry is forwarded, and its answer is still restored', async () => {
  let sent: Request | undefined;
  const original = (async (input: RequestInfo | URL) => {
    sent = input as Request;
    return new Response('data: {"delta":{"text":"[EMAIL_1]"}}\n\n', {
      headers: { 'Content-Type': 'text/event-stream' },
    });
  }) as typeof fetch;

  const { asked, send } = relay({
    mask: masking,
    unmask: () => ({ ok: true, result: { expanded: 'claire@example.fr', tail: '' } }),
  });

  const body = JSON.stringify({ parent_message_uuid: '0d2e-4b6a', model: 'claude' });
  const rendered = await (
    await wrapFetch(original, claudeAi, send)(RETRY_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    })
  ).text();

  assert.ok(sent, 'the retry never left: it was refused as carrying no field to mask');
  assert.equal(await sent!.text(), body, 'the retry body was rewritten; it carries nothing to mask');
  assert.ok(
    rendered.includes('claire@example.fr'),
    `the stored turn's stand-in was rendered to the person: ${rendered}`,
  );
  assert.ok(
    asked.some((ask) => ask.kind === 'unmask'),
    'the answer was not restored, so a retry shows [EMAIL_1] where the original was',
  );
  assert.ok(
    !asked.some((ask) => ask.kind === 'mask'),
    'a retry carries nothing typed, so asking /mask mints a mapping for nothing',
  );
});
