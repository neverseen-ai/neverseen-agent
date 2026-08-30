import { install, type Send } from './intercept.ts';
import {
  type PageAsk,
  type PageMessage,
  type RelayMessage,
  PAGE_SOURCE,
  RELAY_SOURCE,
  type Reply,
} from './protocol.ts';
import { claudeAi } from './site/claude.ts';

// The entry point in the page's own world, at document_start.
//
// It does two things and nothing else: build the bridge to the relay, and install the
// wrappers. Everything it could get wrong lives in modules that a test can drive
// without a browser — which is the same separation the menu bar keeps between what
// decides what to show and the toolkit that shows it.

/**
 * ASK_TIMEOUT_MS bounds how long a send waits on the relay.
 *
 * It exists because the failure it prevents is the one outcome this design must not
 * have: neither masked nor refused. The relay can go away between the post and the
 * answer — an extension update or a reload leaves an open tab with content scripts
 * whose runtime is gone — and a promise nobody settles leaves the site's fetch pending
 * for ever, with no error, no banner and the message box spinning.
 *
 * Thirty seconds because the round trip is a local HTTP call over a few kilobytes and
 * anything near that has already gone wrong; the agent's own client timeouts are of
 * the same order.
 */
const ASK_TIMEOUT_MS = 30_000;

let nextId = 1;
const waiting = new Map<number, (reply: Reply<unknown>) => void>();

window.addEventListener('message', (event: MessageEvent) => {
  if (event.source !== window) return;
  const message = event.data as RelayMessage | undefined;
  if (!message || message.source !== RELAY_SOURCE) return;

  const settle = waiting.get(message.id);
  if (!settle) return;
  waiting.delete(message.id);
  settle(message.reply);
});

const send: Send = (ask: PageAsk) =>
  new Promise<Reply<unknown>>((resolve) => {
    // A note needs no answer, and waiting for one would hold a send open on a message
    // the relay deliberately does not reply to.
    if (ask.kind === 'blocked') {
      postAsk(0, ask);
      resolve({ ok: true, result: null });
      return;
    }

    const id = nextId++;
    const timer = setTimeout(() => {
      // Cleared from the map as well as answered, or a tab whose relay has gone would
      // accumulate one dead entry per send for as long as it stays open.
      waiting.delete(id);
      resolve({
        ok: false,
        reason: 'unreachable',
        message: 'the extension did not answer in time',
      });
    }, ASK_TIMEOUT_MS);

    waiting.set(id, (reply) => {
      clearTimeout(timer);
      resolve(reply);
    });
    postAsk(id, ask);
  });

function postAsk(id: number, ask: PageAsk): void {
  const message: PageMessage = { source: PAGE_SOURCE, id, ask };
  window.postMessage(message, window.location.origin);
}

install(window, claudeAi, send);
