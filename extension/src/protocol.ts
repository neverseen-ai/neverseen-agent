// The shapes that cross the three boundaries this extension has: the page's world
// and the content script, the content script and the service worker, and the service
// worker and the agent.
//
// One file for all three because they are one contract. The page cannot reach
// chrome.runtime and the service worker cannot reach the page, so every ask is
// relayed twice, and a field renamed at one hop and not the other fails silently —
// the ask simply never arrives, and a masking extension that silently stops asking
// is one that forwards in clear.

/** The message tag a page-world post carries, so the relay ignores everything else. */
export const PAGE_SOURCE = 'neverseen:page';

/** The tag a relay's answer carries, so the page world ignores its own posts. */
export const RELAY_SOURCE = 'neverseen:relay';

/**
 * Why an ask failed, in the only four ways a person can act on differently.
 *
 * Distinct rather than one "error", because the options page has to say four
 * different things: install the agent, run one command, reconnect, or a bug. Folded
 * into one message they all read as "it is broken", and the commonest of them —
 * the agent is simply not running — reads as a fault in the extension.
 */
export type Reason =
  /** Nothing answered at the agent's address. It is stopped, or on another port. */
  | 'unreachable'
  /** No control key stored here yet. */
  | 'unconfigured'
  /** The agent refused the key: it was rotated, or it belongs to another agent. */
  | 'unauthorised'
  /** The agent answered, and said no. Its own words come with it. */
  | 'refused';

/**
 * A PageAsk is what the page's world may ask for, and it deliberately cannot name a
 * session.
 *
 * That absence is the security boundary. The interceptor runs in the page's own
 * world, which means everything it can do, a script the site loads can do too: post
 * the same message, with the same tag, and read the answer. Nothing in that world can
 * be authenticated — it *is* the page.
 *
 * So the page is not asked to be trustworthy about the one field that decides whose
 * mapping is read. It says what it wants done; the relay, in the isolated world, says
 * which conversation it is done for, from its own `location`. A page that named the
 * session could name the agent's anonymous "default" one — the session every tool
 * that sends no header shares, which on a workstation is every value the agent has
 * masked since it started — and read it back one guessable token at a time.
 */
export type PageMaskAsk = {
  kind: 'mask';
  texts: string[];
};

export type PageUnmaskAsk = {
  kind: 'unmask';
  text: string;
  tail: string;
  final: boolean;
};

export type HealthAsk = { kind: 'health' };

/**
 * Told rather than asked: the page's world blocked something and the page must say
 * so.
 *
 * It carries which situation it was, never the sentence. The banner is branded and
 * the relay writes its own words for it, because a note carrying arbitrary text is a
 * way for a script on the page to put whatever it likes behind this extension's name.
 */
export type BlockedNote = {
  kind: 'blocked';
  reason: Reason;
  note: Note;
};

/** Note is the closed set of situations a banner may report. */
export type Note = 'send' | 'restore' | 'transport';

export type PageAsk = PageMaskAsk | PageUnmaskAsk | HealthAsk | BlockedNote;

/** MaskAsk and UnmaskAsk are a PageAsk with the session the relay decided on. */
export type MaskAsk = PageMaskAsk & { session: string };
export type UnmaskAsk = PageUnmaskAsk & { session: string };

export type Ask = MaskAsk | UnmaskAsk | HealthAsk | BlockedNote;

/**
 * SESSION_NAMESPACES are the prefixes a session named by this extension may begin
 * with — one per site adapter.
 *
 * Checked in the service worker as well as decided in the relay, and that duplication
 * is deliberate: it is the last thing between a bug in the relay and the agent's own
 * sessions. "default" carries no colon and matches none of these, which is the one
 * name that must never be reachable from a browser.
 */
export const SESSION_NAMESPACES = ['claude:'] as const;

/** namesAWebSession reports whether a session is one this extension may ask about. */
export function namesAWebSession(session: string): boolean {
  return SESSION_NAMESPACES.some((prefix) => session.startsWith(prefix) && session.length > prefix.length);
}

export type MaskAnswer = { texts: string[]; masked: number };
export type UnmaskAnswer = { expanded: string; tail: string };

/** What the agent says about itself, as `proxy.Health` marshals it. */
export type Health = {
  status: string;
  version: string;
  locales: string[];
  substitution: string;
  secret_level: string;
  masking: string;
  providers: string[];
  available_locales?: string[];
  groups?: HealthGroup[];
};

export type HealthGroup = {
  code: string;
  label: string;
  categories: HealthCategory[];
};

export type HealthCategory = {
  code: string;
  label: string;
  off?: boolean;
  locked?: boolean;
};

/**
 * A reply that says which way it went, rather than a value or a thrown error.
 *
 * An exception does not survive the two postMessage hops between the page world and
 * the service worker: what arrives on the other side is a structured clone, and an
 * Error clones to an empty object. So the failure travels as data, with the reason
 * intact — which is the whole point of having reasons.
 */
export type Reply<T> =
  | { ok: true; result: T }
  | { ok: false; reason: Reason; message: string };

/** A page-world post, on its way to the relay. */
export type PageMessage = {
  source: typeof PAGE_SOURCE;
  id: number;
  ask: PageAsk;
};

/** The relay's answer, on its way back. */
export type RelayMessage = {
  source: typeof RELAY_SOURCE;
  id: number;
  reply: Reply<unknown>;
};

/**
 * levelOf is how much of the catalogue the agent is applying, in the three answers
 * `proxy.Status.Level` gives.
 *
 * The indicator follows this rather than "is it masking", for the same reason the
 * menu bar icon and the `neverseen status` exit code do: an agent with a category
 * switched off *is* masking, and a green light over that is a green light over the
 * values that are not being replaced.
 *
 * Read from what the agent said rather than recomputed, with the groups as a
 * fallback for a build whose /healthz predates the field — deriving it is a second
 * answer to a question the agent already answered.
 */
export function levelOf(health: Health | null): 'none' | 'partial' | 'full' {
  if (!health || health.locales.length === 0) return 'none';
  if (health.masking === 'partial' || health.masking === 'full') return health.masking;
  const off = (health.groups ?? []).some((g) => g.categories.some((c) => c.off));
  return off ? 'partial' : 'full';
}
