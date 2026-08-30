import { acceptAsk } from './bridge.ts';
import { noteMessage } from './guidance.ts';
import {
  type PageMessage,
  type RelayMessage,
  PAGE_SOURCE,
  RELAY_SOURCE,
  type Reply,
} from './protocol.ts';
import { claudeAi } from './site/claude.ts';

// The content script in the isolated world: the only hop between the page's own
// globals and the service worker.
//
// It exists because the two ends cannot reach each other. The interceptor has to run
// in the page's world — it replaces window.fetch, and a copy of fetch in an isolated
// world is not the one the site calls — and that world has no chrome.runtime. This
// one has chrome.runtime and no reach into the page's globals. So an ask crosses by
// postMessage here and by extension messaging there.
//
// # What it does not take from the page
//
// It is also the trust boundary, and that is why it decides two things the page used
// to: **which session** an ask acts on, and **what a banner says**.
//
// Neither can be delegated to the page's world, because that world is the page: any
// script the site loads can post what the interceptor posts and read the answer off
// the same channel. Left to the page, the session field made this an oracle — a
// script could name the agent's anonymous "default" session, which carries every
// value masked for every tool that sends no session header, and read it back one
// guessable token at a time. See bridge.ts for what remains and what does not.

/**
 * pageSession is the conversation this tab is showing, decided here and nowhere else.
 *
 * Recomputed per ask rather than captured once, because the site is a single-page app
 * and moves between conversations without a reload.
 */
function pageSession(): string {
  return claudeAi.sessionForPage(new URL(location.href)) ?? fallbackSession;
}

/**
 * fallbackSession is for an address that names no conversation — a new chat, before
 * the site has given it an id.
 *
 * Random, and minted once per page load, for two reasons. Pooled under a fixed name,
 * every new chat in the browser would share one mapping and each could expand the
 * others'. Guessable, it would be the "default" hole again in a smaller costume.
 *
 * TODO: a known ceiling. A value masked before the site rewrites the address to the
 * conversation's own id belongs to this session, so a later turn asking under the real
 * id will not expand it — the replacement shows as a bracket token rather than as the
 * wrong value. Following the site's own navigation would fix it and means listening to
 * history changes, which is more machinery than the case has earned.
 */
const fallbackSession = 'claude:page-' + crypto.randomUUID();

window.addEventListener('message', (event: MessageEvent) => {
  // Same-window posts only. A message from an iframe or another origin arriving here
  // would be a page asking this extension to mask on its behalf, which is a masking
  // oracle for whoever framed us.
  if (event.source !== window) return;

  const message = event.data as PageMessage | undefined;
  if (!message || message.source !== PAGE_SOURCE) return;

  // Validated and rebuilt field by field, with the session stamped on here. A cast
  // would let a `session` the page added ride along, which is the field this whole
  // arrangement exists to take away from it.
  const ask = acceptAsk(message.ask, pageSession());
  if (!ask) {
    // A message shaped like an ask but not one. Answered rather than dropped when it
    // carries an id, because the interceptor is waiting on it and a silent drop is a
    // send that never happens with nothing said about why.
    if (typeof message.id === 'number' && message.id > 0) {
      post(message.id, { ok: false, reason: 'refused', message: 'that is not a request this accepts' });
    }
    return;
  }

  if (ask.kind === 'blocked') {
    // The relay's own words, chosen from the situation the note names. A sentence
    // supplied by the page would be arbitrary text behind this extension's name.
    showBanner(noteMessage(ask.note, ask.reason));
    return;
  }

  try {
    chrome.runtime.sendMessage(ask, (reply: Reply<unknown> | undefined) => {
      // A worker that was torn down mid-ask. Reported as unreachable rather than
      // swallowed: the interceptor is waiting on this to decide whether to send.
      const answered: Reply<unknown> = reply ?? {
        ok: false,
        reason: 'unreachable',
        message: chrome.runtime.lastError?.message ?? 'the extension did not answer',
      };
      post(message.id, answered);
    });
  } catch (err) {
    // sendMessage throws synchronously when the extension context has been
    // invalidated — an update or a reload with this tab still open. The callback
    // above never runs in that case, so without this the ask is dropped and the send
    // hangs for ever: neither masked nor refused, which is the one outcome this
    // design must not have.
    post(message.id, {
      ok: false,
      reason: 'unreachable',
      message: err instanceof Error ? err.message : 'the extension was reloaded',
    });
  }
});

function post(id: number, reply: Reply<unknown>): void {
  const answer: RelayMessage = { source: RELAY_SOURCE, id, reply };
  window.postMessage(answer, window.location.origin);
}

/**
 * showBanner says why something did not happen.
 *
 * A send blocked and nothing said is the worst outcome this extension has: the person
 * retypes their message, or believes the site is broken, and either way the reason —
 * their agent is not running — is the one thing they need to know. The banner names it
 * and stays until it is dismissed.
 */
function showBanner(message: string): void {
  const id = 'cloakfleet-banner';
  document.getElementById(id)?.remove();

  const banner = document.createElement('div');
  banner.id = id;
  banner.textContent = message;
  // Inline rather than a stylesheet: a stylesheet is subject to the page's CSP and
  // this has to appear on a page that blocks one.
  banner.setAttribute(
    'style',
    [
      'position:fixed',
      'z-index:2147483647',
      'top:12px',
      'left:50%',
      'transform:translateX(-50%)',
      'max-width:min(680px,92vw)',
      'padding:12px 40px 12px 16px',
      'border-radius:8px',
      'background:#7f1d1d',
      'color:#fff',
      'font:14px/1.45 system-ui,sans-serif',
      'box-shadow:0 6px 24px rgba(0,0,0,.35)',
      'cursor:pointer',
    ].join(';'),
  );
  banner.title = 'Dismiss';
  banner.addEventListener('click', () => banner.remove());

  (document.body ?? document.documentElement).appendChild(banner);
}
