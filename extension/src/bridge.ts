import { type Ask, type Note, type PageAsk, type Reason } from './protocol.ts';

// What the relay accepts from the page, and what it refuses.
//
// # Why this file exists
//
// The interceptor runs in `world: "MAIN"` because it has to: a copy of `fetch` in an
// isolated world is not the one the site calls. That world is the page's own, and
// everything in it is the page's — its globals, its listeners, and any message it
// posts. A script the site loads, an injected one, or another extension's MAIN-world
// content script can post exactly what the interceptor posts, with the same tag, and
// read the answer off the same channel.
//
// So the relay cannot ask "is this really our interceptor". There is no answer to
// that question, and any secret placed in the page's world to answer it is readable
// by the page. What it can do is stop trusting the page about anything that decides
// *whose data* is read — which is the session, and only the session.
//
// The result is a smaller hole rather than no hole, and that is stated plainly:
// a hostile script on the site can still ask about the conversation the tab is
// actually showing, whose values the page is already being handed to render. What it
// can no longer do is name a different session — another conversation, or the agent's
// anonymous "default" one, which on a workstation carries every value masked for
// every tool that sends no session header.
//
// # Rebuilt, never spread
//
// Each shape below is reconstructed field by field from the raw message. A spread, or
// an `as Ask` cast, would let an extra property ride along — a `session` the page
// added, which is precisely the field this exists to take away from it.

/** MAX_TEXTS bounds one mask ask.
 *
 * A send carries a handful of fields. A message claiming thousands is not a send, and
 * an unbounded list is a way to make the agent do arbitrary work from the page. */
const MAX_TEXTS = 64;

/** REASONS and NOTES are the closed sets a banner may report, so a note cannot
 * smuggle an arbitrary string into a branded message. */
const REASONS: readonly Reason[] = ['unreachable', 'unconfigured', 'unauthorised', 'refused'];
const NOTES: readonly Note[] = ['send', 'restore', 'transport'];

/**
 * acceptAsk validates one message from the page and stamps it with the session the
 * relay decided on, or returns null when it is not something to act on.
 *
 * The session is an argument rather than a field, and that is the whole point: it
 * comes from the relay's own `location`, which the page's message cannot reach.
 */
export function acceptAsk(raw: unknown, session: string): Ask | null {
  if (typeof raw !== 'object' || raw === null) return null;
  const ask = raw as Record<string, unknown>;

  switch (ask.kind) {
    case 'mask': {
      if (!Array.isArray(ask.texts) || ask.texts.length > MAX_TEXTS) return null;
      if (!ask.texts.every((t) => typeof t === 'string')) return null;
      return { kind: 'mask', session, texts: ask.texts as string[] };
    }

    case 'unmask': {
      if (typeof ask.text !== 'string') return null;
      if (typeof ask.tail !== 'string') return null;
      if (typeof ask.final !== 'boolean') return null;
      return { kind: 'unmask', session, text: ask.text, tail: ask.tail, final: ask.final };
    }

    case 'health':
      return { kind: 'health' };

    case 'blocked': {
      // A note carries which situation it was, never the sentence. The relay writes
      // its own words for the banner: a page that supplied them could put anything it
      // liked behind this extension's name, on a page it already controls.
      if (!REASONS.includes(ask.reason as Reason)) return null;
      if (!NOTES.includes(ask.note as Note)) return null;
      return { kind: 'blocked', reason: ask.reason as Reason, note: ask.note as Note };
    }

    default:
      return null;
  }
}

/** isPageAsk is acceptAsk's question without the stamping, for the page side to type
 * what it posts. Exported so the two ends cannot drift about what the shapes are. */
export function isPageAsk(raw: unknown): raw is PageAsk {
  return acceptAsk(raw, 'probe:none') !== null;
}
