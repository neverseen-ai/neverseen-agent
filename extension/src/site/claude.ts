// What this extension knows about one web chat: which request carries what somebody
// typed, which conversation it belongs to, and where the answer's generated text sits
// in a stream event.
//
// Kept apart from everything else because it is the only part that goes stale. The
// agent's routes are a contract in this repository; a site's request shapes are
// somebody else's product, and they move. A second site is a second file of this
// shape and nothing else changed.

/** A Site is everything the interceptor needs to know about one web chat. */
export type Site = {
  /** name is what a message to the person calls this site. */
  readonly name: string;

  /** isSend reports whether a request carries text somebody typed. */
  isSend(url: URL, method: string): boolean;

  /**
   * carriesChat reports whether an address carries conversation traffic at all,
   * whatever the transport.
   *
   * Broader than isSend and asked for a different purpose: isSend picks the request
   * to mask, this one picks the request that must never go out unmasked. The two are
   * not the same set, because the transports that cannot be masked cannot be
   * inspected either — XMLHttpRequest.send and sendBeacon are synchronous, and a
   * WebSocket is open before there is anything to look at. All this can do about one
   * of those is refuse it, and refusing needs only to know that a conversation is on
   * the other end.
   */
  carriesChat(url: URL): boolean;

  /**
   * sessionFor names the conversation, for the X-Session-Id header.
   *
   * Per conversation rather than per browser, because a session is what scopes the
   * mapping: two conversations must not read each other's values, and one
   * conversation has to keep its own across turns. /mask and /unmask for one
   * conversation must return the same name here, or the expansion finds nothing.
   */
  sessionFor(url: URL): string;

  /** texts pulls out every field of the body that carries typed text, in a fixed
   * order. */
  texts(body: unknown): string[];

  /** withTexts puts the masked texts back, positionally, and returns the new body. */
  withTexts(body: unknown, masked: string[]): unknown;

  /** deltaText finds the generated text in one stream event, and how to replace it.
   * Null for an event that carries none — a start, a stop, a usage report. */
  deltaText(event: unknown): { text: string; set(value: string): void } | null;
};

/** claudeAi is claude.ai's web chat. */
export const claudeAi: Site = {
  name: 'Claude',

  isSend(url: URL, method: string): boolean {
    if (method.toUpperCase() !== 'POST') return false;
    // Both the first send and a retry of it: a retry resends the same prompt, and an
    // exemption for it would be a path that reaches the model in clear.
    return (
      this.carriesChat(url) &&
      (url.pathname.endsWith('/completion') || url.pathname.endsWith('/retry_completion'))
    );
  },

  carriesChat(url: URL): boolean {
    return url.pathname.includes('/chat_conversations/');
  },

  sessionFor(url: URL): string {
    const match = /\/chat_conversations\/([^/]+)/.exec(url.pathname);
    // Named rather than falling back to the agent's anonymous "default" session:
    // that one collects every value the agent has handled since it started, and two
    // tabs sharing it is two conversations able to expand each other's tokens.
    return 'claude:' + (match ? match[1] : 'unknown');
  },

  texts(body: unknown): string[] {
    const out: string[] = [];
    for (const field of SEND_FIELDS) {
      const value = (body as Record<string, unknown>)?.[field];
      if (typeof value === 'string') out.push(value);
    }
    return out;
  },

  withTexts(body: unknown, masked: string[]): unknown {
    const next = { ...(body as Record<string, unknown>) };
    let i = 0;
    for (const field of SEND_FIELDS) {
      if (typeof next[field] === 'string') next[field] = masked[i++];
    }
    return next;
  },

  deltaText(event: unknown): { text: string; set(value: string): void } | null {
    const obj = event as Record<string, unknown>;
    if (!obj || typeof obj !== 'object') return null;

    // {"type":"content_block_delta","delta":{"type":"text_delta","text":"…"}}
    const delta = obj.delta as Record<string, unknown> | undefined;
    if (delta && typeof delta === 'object' && typeof delta.text === 'string') {
      return {
        text: delta.text,
        set: (value: string) => {
          delta.text = value;
        },
      };
    }

    // {"type":"completion","completion":"…"} — the older shape, still served.
    if (typeof obj.completion === 'string') {
      return {
        text: obj.completion,
        set: (value: string) => {
          obj.completion = value;
        },
      };
    }

    return null;
  },
};

/**
 * SEND_FIELDS are the body fields that carry what somebody typed.
 *
 * Named rather than "every string in the body", and the difference is what keeps the
 * conversation working: the body also carries a model name, a conversation id and a
 * timezone, and masking those sends the site identifiers it cannot route on.
 *
 * TODO: a known ceiling. Attachments, images and voice are not covered — a pasted
 * file reaches the model through a field this does not read. Declared on the options
 * page rather than left for somebody to discover.
 */
const SEND_FIELDS = ['prompt'] as const;
