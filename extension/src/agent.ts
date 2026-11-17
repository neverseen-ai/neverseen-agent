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
export const DEFAULT_BASE_URL = 'http://127.0.0.1:9787';

/** The header proxy.controlHeader reads. A custom header is what a page cannot set
 * on a simple cross-origin request, which is what keeps the internet out of these
 * routes. */
const CONTROL_HEADER = 'X-Neverseen-Control';

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

/**
 * prove asks whatever answers at the agent's address to show it holds this extension's
 * key, before the key or a text is sent to it.
 *
 * TCP on loopback carries no identity, and a browser cannot ask the kernel who listens:
 * on a machine with several accounts, whoever bound the port first answered there, and
 * received the key and every text this extension sent to be masked. The agent answers
 * with the HMAC, under the key, of a nonce chosen here and the port it was reached on —
 * read from its own socket, so a listener relaying the question to the real agent on
 * another port gets back an answer for that port, and is refused.
 *
 * A wrong proof is reported as a refused key, which it may be: the key rotated, or the
 * address held by something else. Both start with `neverseen key`.
 *
 * TODO: a check, then a send. The browser cannot hold the connection it checked, so a
 * listener that takes the port between the two receives the send. The upgrade is native
 * messaging, whose host runs as this user and can ask the kernel who listens.
 */
export async function prove(cfg: AgentConfig, fetchImpl: Fetch, nonce = freshNonce()): Promise<void> {
  let resp: Response;
  try {
    resp = await fetchImpl(cfg.baseUrl + '/prove', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ nonce }),
    });
  } catch (err) {
    throw new AgentError('unreachable', describe(err));
  }

  if (resp.status === 404) {
    throw new AgentError(
      'refused',
      'the agent at this address cannot prove it holds the key — it is older than this extension; update it',
    );
  }
  if (resp.status === 503) {
    throw new AgentError('unauthorised', await reason(resp));
  }
  if (!resp.ok) {
    throw new AgentError('refused', await reason(resp));
  }
  const answer = (await resp.json().catch(() => null)) as { proof?: unknown } | null;
  if (answer?.proof !== (await proofOf(cfg.key, nonce, portOf(cfg.baseUrl)))) {
    throw new AgentError(
      'unauthorised',
      `what answers at ${cfg.baseUrl} does not hold the key stored here, so nothing was sent to it: ` +
        'the key was rotated, or another program holds this address',
    );
  }
}

/** proofOf is internal/proxy/prove.go proofOf; the contract file holds the two to agreeing. */
export async function proofOf(key: string, nonce: string, port: string): Promise<string> {
  const encoder = new TextEncoder();
  const hmac = await crypto.subtle.importKey('raw', encoder.encode(key), { name: 'HMAC', hash: 'SHA-256' }, false, [
    'sign',
  ]);
  const mac = await crypto.subtle.sign('HMAC', hmac, encoder.encode(`neverseen-prove\0${nonce}\0${port}`));
  return hex(new Uint8Array(mac));
}

/** portOf is the port a base URL dials, its scheme's default included. */
function portOf(baseUrl: string): string {
  const url = new URL(baseUrl);
  return url.port || (url.protocol === 'https:' ? '443' : '80');
}

/** freshNonce is the 32 random bytes /prove signs, as hex. */
function freshNonce(): string {
  return hex(crypto.getRandomValues(new Uint8Array(32)));
}

/** hex is lowercase, as Go's hex.EncodeToString writes the proof this one is compared with. */
function hex(bytes: Uint8Array): string {
  return [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
}

/** How long a proof stands for the calls that follow it. */
const PROOF_STANDS_MS = 2_000;

/** When each address last proved each key, per fetch: a test's own fetch starts with
 * nothing proved, and the worker's one fetch keeps its proofs across calls. */
const proved = new WeakMap<Fetch, Map<string, number>>();

/**
 * proven proves the listener unless the same address proved the same key within
 * PROOF_STANDS_MS. A streamed answer is restored an event at a time, the calls
 * serialised, and a proof before each was a second round trip for every event: the
 * latency of the restoration doubled.
 *
 * TODO: the proof stands for two seconds, so a listener that takes the port within two
 * seconds of the agent leaving it receives what is sent in that time, where a proof per
 * call left it the moment between the check and the send. Any failure drops the proof,
 * which closes it for an agent that stopped answering. The upgrade is native messaging,
 * as for prove.
 */
async function proven(cfg: AgentConfig, fetchImpl: Fetch): Promise<void> {
  let byAddress = proved.get(fetchImpl);
  if (!byAddress) {
    byAddress = new Map();
    proved.set(fetchImpl, byAddress);
  }
  const entry = `${cfg.baseUrl}\0${cfg.key}`;
  const at = byAddress.get(entry);
  if (at !== undefined && Date.now() - at < PROOF_STANDS_MS) return;
  byAddress.delete(entry);
  await prove(cfg, fetchImpl);
  byAddress.set(entry, Date.now());
}

/** forgetProof drops what proven holds for this address and key. */
function forgetProof(cfg: AgentConfig, fetchImpl: Fetch): void {
  proved.get(fetchImpl)?.delete(`${cfg.baseUrl}\0${cfg.key}`);
}

async function call<T>(
  cfg: AgentConfig,
  path: string,
  session: string,
  body: unknown,
  fetchImpl: Fetch,
): Promise<T> {
  if (!cfg.key) {
    throw new AgentError('unconfigured', 'no control key stored — run `neverseen key` and paste it');
  }
  // Before the key, and before the text: the two things a listener of another account
  // must never receive.
  await proven(cfg, fetchImpl);

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
    forgetProof(cfg, fetchImpl);
    throw new AgentError('unreachable', describe(err));
  }

  if (!resp.ok) forgetProof(cfg, fetchImpl);
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
