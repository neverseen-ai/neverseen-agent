import { install, type Send } from './intercept.ts';
import {
  type Ask,
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

const send: Send = (ask: Ask) =>
  new Promise<Reply<unknown>>((resolve) => {
    // A note needs no answer, and waiting for one would hold a send open on a message
    // the relay deliberately does not reply to.
    if (ask.kind === 'blocked') {
      postAsk(0, ask);
      resolve({ ok: true, result: null });
      return;
    }

    const id = nextId++;
    waiting.set(id, resolve);
    postAsk(id, ask);
  });

function postAsk(id: number, ask: Ask): void {
  const message: PageMessage = { source: PAGE_SOURCE, id, ask };
  window.postMessage(message, window.location.origin);
}

install(window, claudeAi, send);
