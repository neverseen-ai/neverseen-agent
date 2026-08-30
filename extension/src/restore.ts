import { type UnmaskAnswer } from './protocol.ts';
import { type Site } from './site/claude.ts';

// Putting the originals back into a streamed answer, from the client side of the
// agent's /unmask route.
//
// This is the streaming rehydrator's problem seen from the browser, and it has the
// same shape for the same reason. Generated text arrives in pieces of a few
// characters, so a replacement the model echoed is regularly split across two of
// them: the page would render "[EMA" from one event and "IL_1]" from the next, and
// neither can be expanded alone. Buffering the whole answer would fix it and defeat
// the point — the page is showing text as it arrives.
//
// So the agent hands back a tail with every chunk, this holds it, and the next chunk
// goes out with it prepended. The tail travels with the client rather than being kept
// by the agent, which is what lets two conversations in two tabs share one agent
// without a per-session buffer keyed by a name the caller chooses.
//
// Two rules this must not lose:
//
//   - **The event is rewritten structurally, never as raw bytes.** An original can
//     carry a quote or a newline, and splicing one into the raw payload produces an
//     event the page cannot parse. The payload is decoded, the generated text
//     replaced in the decoded object, and the encoder does the escaping.
//   - **The calls are serialised.** Chunk n's tail prefixes chunk n+1, so two in
//     flight at once is a corruption rather than an optimisation. A TransformStream
//     awaits each transform before calling the next, which is exactly that
//     guarantee — and the test holds it by answering out of order.

/** Ask is the one thing this needs from the outside: a round trip to /unmask. */
export type Ask = (chunk: { text: string; tail: string; final: boolean }) => Promise<UnmaskAnswer>;

/** EVENT_SEPARATOR ends a server-sent event. */
const EVENT_SEPARATOR = '\n\n';

/**
 * restoreStream wraps a server-sent event body so the page reads originals.
 *
 * Complete events only: a chunk that ends mid-event is held until its separator
 * arrives, because half an event is not something to parse and not something to hand
 * the page.
 */
export function restoreStream(ask: Ask, site: Site): TransformStream<Uint8Array, Uint8Array> {
  const decoder = new TextDecoder();
  const encoder = new TextEncoder();

  let buffered = '';
  let tail = '';

  // The last event that carried generated text, kept so a held-back tail still has an
  // event to travel in when the stream ends mid-replacement. A bare fragment is not a
  // valid event and the page would drop it.
  let template: string | null = null;

  async function rewrite(raw: string): Promise<string> {
    const payload = payloadOf(raw);
    if (payload === null) return raw;

    let event: unknown;
    try {
      event = JSON.parse(payload);
    } catch {
      // The "[DONE]" sentinel, or a shape this does not model. Left exactly as it
      // arrived: there is no structure to work with, and rewriting the raw text is
      // the thing the escaping rule forbids.
      //
      // TODO: a known ceiling. A replacement echoed in an event whose payload is not
      // JSON reaches the page unexpanded — visible as a bracket token rather than
      // wrong, which is the safe direction to fail in.
      return raw;
    }

    const delta = site.deltaText(event);
    if (!delta) {
      // A structured event carrying no generated text: a start, a stop, a usage
      // report. Nothing to expand and nothing to hold back.
      //
      // TODO: a replacement in some other string of such an event is not expanded.
      // The agent's own rehydrator maps every string in the event; doing the same
      // here is a round trip per string, and the field that carries model output is
      // the one this reads.
      return raw;
    }

    const answer = await ask({ text: delta.text, tail, final: false });
    tail = answer.tail;
    delta.set(answer.expanded);

    template = raw;
    return withPayload(raw, JSON.stringify(event));
  }

  /** flush emits whatever is still held back when the stream ends. */
  async function flush(): Promise<string> {
    const answer = await ask({ text: '', tail, final: true });
    tail = '';
    if (!answer.expanded || template === null) return '';

    // In a copy of the last event that carried text, for the reason the agent's own
    // flush uses one: a bare fragment is not a valid event.
    const payload = payloadOf(template);
    if (payload === null) return '';
    let event: unknown;
    try {
      event = JSON.parse(payload);
    } catch {
      return '';
    }
    const delta = site.deltaText(event);
    if (!delta) return '';
    delta.set(answer.expanded);
    return withPayload(template, JSON.stringify(event));
  }

  return new TransformStream<Uint8Array, Uint8Array>({
    async transform(chunk, controller) {
      buffered += decoder.decode(chunk, { stream: true });

      let cut = buffered.indexOf(EVENT_SEPARATOR);
      while (cut !== -1) {
        const raw = buffered.slice(0, cut + EVENT_SEPARATOR.length);
        buffered = buffered.slice(cut + EVENT_SEPARATOR.length);
        controller.enqueue(encoder.encode(await rewrite(raw)));
        cut = buffered.indexOf(EVENT_SEPARATOR);
      }
    },

    async flush(controller) {
      buffered += decoder.decode();
      if (buffered !== '') {
        // A last event with no trailing separator. Rewritten like any other rather
        // than dropped: it is the caller's own text.
        controller.enqueue(encoder.encode(await rewrite(buffered)));
        buffered = '';
      }
      const held = await flush();
      if (held !== '') controller.enqueue(encoder.encode(held));
    },
  });
}

/** payloadOf returns the JSON a "data:" line carries, or null when there is none. */
function payloadOf(raw: string): string | null {
  for (const line of raw.split('\n')) {
    if (line.startsWith('data:')) return line.slice('data:'.length).trim();
  }
  return null;
}

/** withPayload rebuilds an event with a new "data:" line, keeping every other line —
 * the event name above it is what a client dispatches on. */
function withPayload(raw: string, payload: string): string {
  let replaced = false;
  const lines = raw.split('\n').map((line) => {
    if (!replaced && line.startsWith('data:')) {
      replaced = true;
      return 'data: ' + payload;
    }
    return line;
  });
  return lines.join('\n');
}
