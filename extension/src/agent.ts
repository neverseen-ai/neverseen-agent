import {
  type Health,
  type MaskAnswer,
  type Reason,
  type UnmaskAnswer,
} from './protocol.ts';

// The client for the local agent's routes. It is the only thing in this extension
// that knows how to talk to it, and it holds nothing else: no catalogue, no policy,
// no mappings. One engine, one policy, one set of surfaces.
//
// It runs in the service worker, which is where the control key lives. The page's
// world must never see the key: a key readable from the page is a key any script the
// site loads can read, and it opens /unmask — token in, original out.

/** DEFAULT_BASE_URL mirrors proxy.DefaultListen. */
export const DEFAULT_BASE_URL = 'http://127.0.0.1:8787';

/** The header proxy.controlHeader reads. A custom header is what a page cannot set
 * on a simple cross-origin request, which is what keeps the internet out of these
 * routes. */
const CONTROL_HEADER = 'X-Cloakfleet-Control';

/** The header proxy.sessionOf reads. /mask and /unmask for one conversation must
 * name the same session, or the expansion finds nothing. */
const SESSION_HEADER = 'X-Session-Id';

export type AgentConfig = {
  baseUrl: string;
  key: string;
};

/** AgentError carries the reason a person can act on, alongside the agent's own
 * words — which say which category it refused and why, and are the only part
 * somebody can do anything about. */
export class AgentError extends Error {
  readonly reason: Reason;

  constructor(reason: Reason, message: string) {
    super(message);
    this.name = 'AgentError';
    this.reason = reason;
  }
}

/** Fetch is the shape this module needs, so a test can drive it without a network
 * and the service worker can pass the real one. */
export type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

/**
 * mask replaces the sensitive values in a list of texts, in one pass.
 *
 * A list rather than a call per field: a message being sent carries several text
 * fields, and one pass is what gives a value repeated across two of them one
 * identity. Sent separately the same address would leave as two different people.
 */
export async function mask(
  cfg: AgentConfig,
  session: string,
  texts: string[],
  fetchImpl: Fetch,
): Promise<MaskAnswer> {
  const answer = await call<MaskAnswer>(cfg, '/mask', session, { texts }, fetchImpl);
  if (!Array.isArray(answer.texts) || answer.texts.length !== texts.length) {
    // Positional, so a short list would put one field's masked text into another
    // field. Refused rather than patched up: the send is blocked, which is the safe
    // direction, and a silent misalignment is not.
    throw new AgentError('refused', 'the agent returned a different number of texts than it was sent');
  }
  return answer;
}

/**
 * unmask expands one chunk of a streamed answer, carrying the tail the caller holds.
 *
 * The tail travels with the client rather than being held by the agent, which is what
 * keeps the route stateless. `final` ends the stream: the remaining tail is expanded
 * and nothing is held back. Without it a value masked at the very end of an answer is
 * simply never shown.
 */
export async function unmask(
  cfg: AgentConfig,
  session: string,
  chunk: { text: string; tail: string; final: boolean },
  fetchImpl: Fetch,
): Promise<UnmaskAnswer> {
  return call<UnmaskAnswer>(cfg, '/unmask', session, chunk, fetchImpl);
}

/**
 * health asks what the agent is applying.
 *
 * Unauthenticated, as the route is, so the options page can tell "the agent is not
 * running" from "the key is wrong" — two states that look identical if the only
 * thing this can ask needs a key.
 */
export async function health(baseUrl: string, fetchImpl: Fetch): Promise<Health> {
  let resp: Response;
  try {
    resp = await fetchImpl(baseUrl + '/healthz', { method: 'GET' });
  } catch (err) {
    throw new AgentError('unreachable', describe(err));
  }
  if (!resp.ok) {
    throw new AgentError('refused', await reason(resp));
  }
  return (await resp.json()) as Health;
}

async function call<T>(
  cfg: AgentConfig,
  path: string,
  session: string,
  body: unknown,
  fetchImpl: Fetch,
): Promise<T> {
  if (!cfg.key) {
    throw new AgentError('unconfigured', 'no control key stored — run `cloakfleet key` and paste it');
  }

  let resp: Response;
  try {
    resp = await fetchImpl(cfg.baseUrl + path, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        [CONTROL_HEADER]: cfg.key,
        [SESSION_HEADER]: session,
      },
      body: JSON.stringify(body),
    });
  } catch (err) {
    // A fetch that rejects is the agent being stopped, or bound somewhere else.
    // Never a reason to carry on: the send this was masking is blocked.
    throw new AgentError('unreachable', describe(err));
  }

  if (resp.status === 403 || resp.status === 503) {
    // 403 is a key this agent does not accept; 503 is an agent that has no key of
    // its own. Both are "reconnect" for a person, and neither is "it is down".
    throw new AgentError('unauthorised', await reason(resp));
  }
  if (!resp.ok) {
    throw new AgentError('refused', await reason(resp));
  }
  return (await resp.json()) as T;
}

/** reason is the agent's own words, which say what it refused and why. */
async function reason(resp: Response): Promise<string> {
  const text = (await resp.text().catch(() => '')).trim();
  return text || `the agent answered ${resp.status}`;
}

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
