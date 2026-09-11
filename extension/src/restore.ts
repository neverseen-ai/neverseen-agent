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

/** EVENT_SEPARATOR ends a server-sent event: a blank line, in either line ending the
 * spec allows. Matched on "\n\n" alone, a CRLF stream never yields a complete event
 * and the whole answer is held until the flush. */
const EVENT_SEPARATOR = /\r?\n\r?\n/;

/** LINE_BREAK splits an event into its lines, whichever ending the server used. */
const LINE_BREAK = /\r?\n/;

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
      return (await release()) + raw;
    }

    const delta = site.deltaText(event);
    if (!delta) {
      // A structured event carrying no generated text: a start, a stop, a usage
      // report. Nothing to expand — and whatever the block before it was holding is
      // released *before* it, never after. A tail belongs to the block it was held
      // back from, as the agent's closeBlock has it: a client that stops appending
      // on the stop event would otherwise lose the last characters of the answer,
      // because the flush comes after the event it stopped on.
      //
      // A keep-alive is not a block boundary, and releasing on one would render a
      // replacement it happened to split as two halves.
      //
      // TODO: a replacement in some other string of such an event is not expanded.
      // The agent's own rehydrator maps every string in the event; doing the same
      // here is a round trip per string, and the field that carries model output is
      // the one this reads.
      if (isKeepAlive(event)) return raw;
      return (await release()) + raw;
    }

    const answer = await ask({ text: delta.text, tail, final: false });
    tail = answer.tail;
    delta.set(answer.expanded);

    template = raw;
    return withPayload(raw, JSON.stringify(event));
  }

  /** release emits whatever is still held back, as an event of its own — at the end
   * of the stream, or before an event that closes the block it belongs to. */
  async function release(): Promise<string> {
    // Nothing held means nothing to ask: a round trip per stop event, to expand an
    // empty string, is a call the agent counts and the person waits on.
    if (tail === '') return '';

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

      let cut = EVENT_SEPARATOR.exec(buffered);
      while (cut !== null) {
        const end = cut.index + cut[0].length;
        const raw = buffered.slice(0, end);
        buffered = buffered.slice(end);
        controller.enqueue(encoder.encode(await rewrite(raw)));
        cut = EVENT_SEPARATOR.exec(buffered);
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
      const held = await release();
      if (held !== '') controller.enqueue(encoder.encode(held));
    },
  });
}

/** isKeepAlive reports a ping: an event that keeps the connection open and closes
 * nothing. */
function isKeepAlive(event: unknown): boolean {
  return typeof event === 'object' && event !== null && (event as { type?: unknown }).type === 'ping';
}

/** payloadOf returns the JSON the "data:" lines carry, or null when there is none.
 *
 * All of them, joined by a newline, which is what the spec says a client receives:
 * a server may split one payload over several "data:" lines, and reading the first
 * alone hands the parser half a document. */
function payloadOf(raw: string): string | null {
  const parts: string[] = [];
  for (const line of raw.split(LINE_BREAK)) {
    if (line.startsWith('data:')) parts.push(line.slice('data:'.length).trim());
  }
  return parts.length === 0 ? null : parts.join('\n');
}

/** withPayload rebuilds an event with one new "data:" line in place of the ones it
 * had, keeping every other line — the event name above it is what a client
 * dispatches on — and the line ending the server used. */
function withPayload(raw: string, payload: string): string {
  const eol = raw.includes('\r\n') ? '\r\n' : '\n';
  let replaced = false;
  const lines: string[] = [];
  for (const line of raw.split(LINE_BREAK)) {
    if (!line.startsWith('data:')) {
      lines.push(line);
    } else if (!replaced) {
      replaced = true;
      lines.push('data: ' + payload);
    }
  }
  return lines.join(eol);
}
