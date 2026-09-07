import {
  type MaskAnswer,
  type Note,
  type PageAsk,
  type Reason,
  type Reply,
  type UnmaskAnswer,
} from './protocol.ts';
import { blockedMessage, noteMessage } from './guidance.ts';
import { restoreStream } from './restore.ts';
import { type Site } from './site/claude.ts';

// Wrapping the page's own network calls, in the page's own world.
//
// This is the one technique manifest v3 leaves. Blocking webRequest is gone and
// declarativeNetRequest cannot touch a request or response body, so the only place
// left to stand is inside the page: replace window.fetch before the site's script
// runs, and the site's own calls come through here.
//
// It holds no engine and no key. Everything it needs is one round trip away, through
// the relay and into the service worker, which is where the key lives.
//
// # Fail closed
//
// Every path out of here either masks or refuses. A send whose masking failed is not
// sent, and the person is told why — this is the extension's mirror of the proxy's
// 415 on a body it cannot read, and it is the rule somebody will be tempted to
// soften, because a blocked send looks like a broken site and a forwarded one looks
// like nothing at all.

/**
 * Send is one round trip to the service worker, through the relay.
 *
 * A PageAsk, which cannot name a session: this code runs in the page's own world, so
 * anything it could say about whose mapping to read, a script the site loads could say
 * too. The relay decides that from its own location — see bridge.ts.
 */
export type Send = (ask: PageAsk) => Promise<Reply<unknown>>;

/** Blocked is what a refusal throws, so the caller can say why. */
export class Blocked extends Error {
  readonly reason: Reason;

  constructor(reason: Reason, message: string) {
    super(message);
    this.name = 'Blocked';
    this.reason = reason;
  }
}

/**
 * install replaces the page's network entry points and returns nothing to undo it.
 *
 * At document_start, before the site's own script runs. A site that captured fetch
 * into a local before this ran would keep calling the original, so the ordering is
 * the whole guarantee — and it is why the interceptor is a content script rather than
 * anything injected later.
 */
export function install(target: Window & typeof globalThis, site: Site, send: Send): void {
  const originalFetch = target.fetch.bind(target);
  target.fetch = wrapFetch(originalFetch, site, send);

  guardXHR(target, site, send);
  guardBeacon(target, site, send);
  guardWebSocket(target, site, send);
}

/** wrapFetch is the whole masked path: mask what goes out, restore what comes back. */
export function wrapFetch(original: typeof fetch, site: Site, send: Send): typeof fetch {
  return async function neverseenFetch(
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> {
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();

    let url: URL;
    try {
      url = new URL(input instanceof Request ? input.url : String(input), location.href);
    } catch {
      // Not an address this can reason about. Everything this extension does is
      // scoped to one site's chat, so anything unrecognisable is somebody else's
      // traffic and is left exactly alone.
      return original(input, init);
    }

    if (!site.isSend(url, method)) return original(input, init);

    let raw: string;
    try {
      raw = await bodyText(input, init);
    } catch (err) {
      // A send whose body cannot be read is refused, not forwarded. This is the same
      // decision the agent makes on a body it cannot decode, and for the same reason:
      // forwarding what could not be inspected is the one failure a data-loss control
      // must never have.
      throw block(send, 'refused', 'send',
        'Neverseen: your message was not sent — its contents could not be read, so nothing could mask them.',
        err);
    }

    let body: unknown;
    try {
      body = JSON.parse(raw);
    } catch (err) {
      throw block(send, 'refused', 'send',
        'Neverseen: your message was not sent — it is not in a shape this extension knows how to mask.',
        err);
    }

    const texts = site.texts(body);
    let outgoing = raw;
    if (texts.length > 0) {
      let masked: MaskAnswer;
      try {
        masked = await ask<MaskAnswer>(send, { kind: 'mask', texts });
      } catch (err) {
        // Every way the masking can fail comes through here, and every one of them
        // blocks the send *and* says why. A rejection on its own reads to the site as
        // a network failure and to the person as a broken page — while the actual
        // cause, most often an agent that is not running, is one command away.
        const reason = err instanceof Blocked ? err.reason : 'refused';
        throw block(send, reason, 'send', blockedMessage(reason, describe(err)), err);
      }
      outgoing = JSON.stringify(site.withTexts(body, masked.texts));
    }

    const response = await original(rebuild(input, init, outgoing));
    return restore(response, site, send);
  };
}

/**
 * restore wraps a streamed answer so the page renders originals.
 *
 * Wrapped whether or not this turn masked anything: the conversation's mapping
 * outlives the turn that minted it, so an answer echoing a value from three messages
 * ago still arrives with a replacement in it.
 */
function restore(response: Response, site: Site, send: Send): Response {
  const type = response.headers.get('Content-Type') ?? '';
  if (!type.includes('text/event-stream') || response.body === null) return response;

  // Said once per stream rather than per chunk: an agent that stopped mid-answer
  // fails every remaining chunk, and a banner per chunk is a wall of the same
  // sentence.
  let told = false;

  const stream = response.body.pipeThrough(
    restoreStream(async (chunk) => {
      try {
        return await ask<UnmaskAnswer>(send, {
          kind: 'unmask',
          text: chunk.text,
          tail: chunk.tail,
          final: chunk.final,
        });
      } catch (err) {
        // Deliberately the opposite direction from the outbound half, and the
        // asymmetry is the point. Failing closed on the way out stops a value
        // reaching the model; failing closed on the way back would throw away an
        // answer that has already been paid for and already arrived, to prevent
        // nothing — unexpanded text is the caller's own replacement showing as
        // [EMAIL_1], which is unreadable rather than unsafe.
        if (!told) {
          told = true;
          const reason = err instanceof Blocked ? err.reason : 'refused';
          // The situation, never the sentence: the relay writes the words, so a
          // script on the page cannot put text of its own behind this extension's
          // name. See bridge.ts.
          void send({ kind: 'blocked', reason, note: 'restore' });
        }
        return { expanded: chunk.tail + chunk.text, tail: '' };
      }
    }, site),
  );

  // A new Response over the transformed stream, keeping the status and the headers:
  // the site reads both, and a stream handed back under a fabricated 200 would hide
  // an error the site knows how to show.
  return new Response(stream, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
}

/** ask sends one request through the relay and unwraps the reply, throwing Blocked. */
async function ask<T>(send: Send, request: PageAsk): Promise<T> {
  const reply = await send(request);
  if (!reply.ok) throw new Blocked(reply.reason, reply.message);
  return reply.result as T;
}

/** block tells the page why nothing was sent, and returns the error to throw.
 *
 * Both halves, always. A rejected fetch alone reads to the site as a network failure
 * and to the person as a site that is broken — and the actual cause, an agent that is
 * not running, is a thing they can fix in one command. */
function block(send: Send, reason: Reason, note: Note, message: string, cause?: unknown): Blocked {
  // The note says which situation it was; the relay turns that into the sentence the
  // banner shows. `message` stays here, on the error the site sees, where it is never
  // rendered under this extension's name.
  void send({ kind: 'blocked', reason, note });
  const error = new Blocked(reason, message);
  if (cause !== undefined) error.cause = cause;
  return error;
}

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** bodyText reads what a send is carrying, from whichever of the two shapes fetch was
 * called in. */
async function bodyText(input: RequestInfo | URL, init?: RequestInit): Promise<string> {
  if (typeof init?.body === 'string') return init.body;
  if (init?.body !== undefined && init?.body !== null) {
    // A FormData, a Blob, a stream. Not a shape this masks, and reading it would
    // consume it — so it goes to the caller as an unreadable body, which is refused.
    throw new Error('the body is not text');
  }
  if (input instanceof Request) return input.clone().text();
  throw new Error('there is no body to read');
}

/** rebuild returns the arguments to call the original fetch with, carrying the masked
 * body and nothing else changed. */
function rebuild(
  input: RequestInfo | URL,
  init: RequestInit | undefined,
  body: string,
): Request {
  // Through a Request either way, so the credentials, the mode, the referrer policy
  // and every header the site set travel exactly as it set them. Rebuilding an init
  // by hand is how a wrapper drops the cookie and the send starts failing to
  // authenticate.
  return input instanceof Request && init === undefined
    ? new Request(input, { body })
    : new Request(input as RequestInfo, { ...init, body });
}

/**
 * guardXHR refuses a chat request sent over XMLHttpRequest.
 *
 * Refused rather than masked, because it cannot be masked: send() is synchronous and
 * the round trip to the agent is not. A site that moved its chat here would otherwise
 * go on working with this extension installed and mask nothing at all, which is the
 * one failure mode worse than a blocked send.
 */
function guardXHR(target: Window & typeof globalThis, site: Site, send: Send): void {
  const proto = target.XMLHttpRequest.prototype;
  const open = proto.open;
  const originalSend = proto.send;
  const marked = new WeakSet<XMLHttpRequest>();

  proto.open = function (this: XMLHttpRequest, method: string, url: string | URL, ...rest: unknown[]) {
    try {
      if (site.carriesChat(new URL(String(url), location.href))) marked.add(this);
    } catch {
      // An address that will not parse is not one of ours.
    }
    return (open as (...args: unknown[]) => void).call(this, method, url, ...rest);
  } as typeof proto.open;

  proto.send = function (this: XMLHttpRequest, ...args: unknown[]) {
    if (marked.has(this)) {
      throw block(send, 'refused', 'transport', noteMessage('transport', 'refused'));
    }
    return (originalSend as (...a: unknown[]) => void).apply(this, args);
  } as typeof proto.send;
}

/** guardBeacon refuses a chat request sent over sendBeacon, for the reason guardXHR
 * refuses one over XMLHttpRequest: it is fire-and-forget and synchronous, so there is
 * no moment at which it could be masked. */
function guardBeacon(target: Window & typeof globalThis, site: Site, send: Send): void {
  const original = target.navigator.sendBeacon?.bind(target.navigator);
  if (!original) return;

  target.navigator.sendBeacon = function (url: string | URL, data?: BodyInit | null): boolean {
    try {
      if (site.carriesChat(new URL(String(url), location.href))) {
        block(send, 'refused', 'transport', noteMessage('transport', 'refused'));
        return false;
      }
    } catch {
      // Not an address this can reason about.
    }
    return original(url, data);
  };
}

/** guardWebSocket refuses a chat connection, for the same reason again: the socket is
 * open before there is anything to inspect, and frames go out without ever passing
 * through a place this could stand. */
function guardWebSocket(target: Window & typeof globalThis, site: Site, send: Send): void {
  const Original = target.WebSocket;
  if (!Original) return;

  const Guarded = function (this: unknown, url: string | URL, protocols?: string | string[]) {
    try {
      if (site.carriesChat(new URL(String(url), location.href))) {
        throw block(send, 'refused', 'transport', noteMessage('transport', 'refused'));
      }
    } catch (err) {
      if (err instanceof Blocked) throw err;
      // Not an address this can reason about.
    }
    return new Original(url as string, protocols);
  } as unknown as typeof WebSocket;

  Guarded.prototype = Original.prototype;
  target.WebSocket = Guarded;
}
