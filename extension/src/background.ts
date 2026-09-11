import { AgentError, mask, unmask } from './agent.ts';
import { acceptAsk } from './bridge.ts';
import { type Ask, namesAWebSession, type Reply } from './protocol.ts';
import { load, type Storage } from './settings.ts';

// The service worker: the one place that talks to the agent, and the one place that
// holds the key.
//
// It is here rather than in the content script because of what the page can reach. A
// content script in the page's own world shares its globals; one in an isolated world
// does not, but the page's Content-Security-Policy still governs what it may connect
// to, and claude.ai's does not list 127.0.0.1. Extension messaging is not subject to
// either, so the ask crosses into the worker and the fetch happens where the page has
// no reach at all.
//
// The key never leaves this file. A key readable from the page is a key any script
// the site loads can read, and it opens /unmask — token in, original out.

chrome.runtime.onMessage.addListener((message: unknown, sender, sendResponse) => {
  // Our own relay and nothing else. onMessage also receives what another extension
  // sends this one's id, and an ask from there would be a way around the relay's
  // choice of session — the field the whole boundary exists to keep out of reach.
  if (sender.id !== chrome.runtime.id) return;

  // Rebuilt field by field rather than cast: a cast believes every property that
  // arrived, and the relay is one hop that can have a bug in it.
  const ask = acceptFromRelay(message);
  if (!ask) {
    sendResponse({ ok: false, reason: 'refused', message: 'that is not a request this accepts' });
    return;
  }

  // The listener answers asynchronously, which chrome signals by returning true.
  // Without it the channel closes before the agent has answered and the send is
  // blocked on a round trip that did in fact succeed.
  answer(ask)
    .then(sendResponse)
    .catch((err: unknown) => sendResponse(failure(err)));
  return true;
});

/** acceptFromRelay validates a message the relay sent, which is a page ask with the
 * session the relay stamped on it — so the relay's own validator does the work,
 * with the session read out of the message and checked by answer() as before. */
function acceptFromRelay(raw: unknown): Ask | null {
  if (typeof raw !== 'object' || raw === null) return null;
  const session = (raw as Record<string, unknown>).session;
  if (typeof session !== 'string') return null;
  return acceptAsk(raw, session);
}

async function answer(ask: Ask): Promise<Reply<unknown>> {
  const storage = chrome.storage.local as unknown as Storage;
  try {
    // The relay is the only thing that names a session, and this is the check that
    // holds even if it stops being. A browser must never be able to ask about the
    // agent's anonymous "default" session — the one every tool that sends no header
    // shares — so the namespace is asserted here too rather than trusted one hop away.
    if ((ask.kind === 'mask' || ask.kind === 'unmask') && !namesAWebSession(ask.session)) {
      return { ok: false, reason: 'refused', message: 'that is not a session this extension may name' };
    }

    switch (ask.kind) {
      case 'mask': {
        const cfg = await load(storage);
        return { ok: true, result: await mask(cfg, ask.session, ask.texts, fetch) };
      }
      case 'unmask': {
        const cfg = await load(storage);
        return {
          ok: true,
          result: await unmask(
            cfg,
            ask.session,
            { text: ask.text, tail: ask.tail, final: ask.final },
            fetch,
          ),
        };
      }
      default:
        return { ok: false, reason: 'refused', message: 'unknown request' };
    }
  } catch (err) {
    return failure(err);
  }
}

/** failure turns a thrown error into the reply shape, keeping the reason.
 *
 * As data rather than as an exception, because what crosses chrome.runtime is a
 * structured clone and an Error clones to an empty object — the reason, which is the
 * whole point of having reasons, would arrive as undefined. */
function failure(err: unknown): Reply<never> {
  if (err instanceof AgentError) {
    return { ok: false, reason: err.reason, message: err.message };
  }
  return { ok: false, reason: 'refused', message: err instanceof Error ? err.message : String(err) };
}
