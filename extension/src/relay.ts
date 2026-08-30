import {
  type PageMessage,
  type RelayMessage,
  PAGE_SOURCE,
  RELAY_SOURCE,
  type Reply,
} from './protocol.ts';

// The content script in the isolated world: the only hop between the page's own
// globals and the service worker.
//
// It exists because the two ends cannot reach each other. The interceptor has to run
// in the page's world — it replaces window.fetch, and a copy of fetch in an isolated
// world is not the one the site calls — and that world has no chrome.runtime. This
// one has chrome.runtime and no reach into the page's globals. So an ask crosses by
// postMessage here and by extension messaging there.
//
// It also owns what the person sees when a send is blocked. The page's world could
// write to the DOM too, but the banner is the extension talking about itself, and
// keeping it out of the page's world keeps it out of reach of the site's own script.

window.addEventListener('message', (event: MessageEvent) => {
  // Same-window posts only. A message from an iframe or another origin arriving here
  // would be a page asking this extension to mask on its behalf, which is a masking
  // oracle for whoever framed us.
  if (event.source !== window) return;

  const message = event.data as PageMessage | undefined;
  if (!message || message.source !== PAGE_SOURCE) return;

  if (message.ask.kind === 'blocked') {
    showBanner(message.ask.message);
    return;
  }

  chrome.runtime.sendMessage(message.ask, (reply: Reply<unknown> | undefined) => {
    // A worker that was torn down mid-ask, or an extension that has just been
    // reloaded. Reported as unreachable rather than swallowed: the interceptor is
    // waiting on this to decide whether to send, and a silent drop is a send that
    // never happens with nothing said about why.
    const answered: Reply<unknown> = reply ?? {
      ok: false,
      reason: 'unreachable',
      message: chrome.runtime.lastError?.message ?? 'the extension did not answer',
    };
    post(message.id, answered);
  });
});

function post(id: number, reply: Reply<unknown>): void {
  const answer: RelayMessage = { source: RELAY_SOURCE, id, reply };
  window.postMessage(answer, window.location.origin);
}

/**
 * showBanner says why a send did not happen.
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
