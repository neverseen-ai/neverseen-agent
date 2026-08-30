import assert from 'node:assert/strict';
import { test } from 'node:test';

import { type UnmaskAnswer } from '../src/protocol.ts';
import { restoreStream, type Ask } from '../src/restore.ts';
import { claudeAi } from '../src/site/claude.ts';

// The inbound half, driven without a browser.
//
// The agent is stubbed by a tiny expander with the same contract: expand what it
// knows, hold back what could be the start of something it knows. It is not a second
// engine — it answers three fixed mappings — and the tests below are about the
// stream, not about detection.

/** expander is /unmask's contract, small enough to reason about. */
function expander(known: Record<string, string>): Ask {
  return async ({ text, tail, final }) => {
    const combined = tail + text;
    let expanded = combined;
    for (const [token, original] of Object.entries(known)) {
      expanded = expanded.split(token).join(original);
    }
    if (final) return { expanded, tail: '' };

    // Hold back a trailing prefix of any token, longest first — TailLen's rule.
    for (const token of Object.keys(known).sort((a, b) => b.length - a.length)) {
      for (let n = token.length - 1; n > 0; n--) {
        if (expanded.endsWith(token.slice(0, n))) {
          return { expanded: expanded.slice(0, -n), tail: expanded.slice(-n) };
        }
      }
    }
    return { expanded, tail: '' };
  };
}

/** through pushes chunks into the transform and returns everything that came out. */
async function through(chunks: string[], ask: Ask): Promise<string> {
  const encoder = new TextEncoder();
  const source = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });

  const out: string[] = [];
  const decoder = new TextDecoder();
  const reader = source.pipeThrough(restoreStream(ask, claudeAi)).getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    out.push(decoder.decode(value, { stream: true }));
  }
  return out.join('');
}

/** deltas renders a list of texts as Anthropic-shaped events. */
function deltas(...texts: string[]): string[] {
  return texts.map(
    (text) =>
      'event: content_block_delta\n' +
      `data: ${JSON.stringify({ type: 'content_block_delta', delta: { type: 'text_delta', text } })}\n\n`,
  );
}

/** textsOf pulls the generated text back out of a rendered stream, which is what a
 * client concatenating deltas would end up showing. */
function textsOf(stream: string): string {
  let out = '';
  for (const line of stream.split('\n')) {
    if (!line.startsWith('data:')) continue;
    const payload = line.slice('data:'.length).trim();
    try {
      const event = JSON.parse(payload) as { delta?: { text?: string }; completion?: string };
      out += event.delta?.text ?? event.completion ?? '';
    } catch {
      /* the [DONE] sentinel */
    }
  }
  return out;
}

const known = { '[EMAIL_1]': 'claire@example.fr' };

test('a replacement inside one event is expanded', async () => {
  const stream = await through(deltas('write to [EMAIL_1] today'), expander(known));
  assert.equal(textsOf(stream), 'write to claire@example.fr today');
});

test('a replacement split across two events is restored whole', async () => {
  // The case the tail exists for. Neither event can be expanded on its own, and an
  // extension that only ever looked at one at a time would render the person their
  // own bookkeeping, in halves.
  const stream = await through(deltas('write to [EMA', 'IL_1] today'), expander(known));
  assert.equal(textsOf(stream), 'write to claire@example.fr today');
});

test('a replacement split across three events is restored whole', async () => {
  const stream = await through(deltas('write to [EM', 'AIL', '_1] today'), expander(known));
  assert.equal(textsOf(stream), 'write to claire@example.fr today');
});

test('a replacement ending the stream is restored by the flush', async () => {
  // Without the final call the last event's held-back tail is simply dropped and the
  // person reads a sentence with its last word missing.
  const stream = await through(deltas('her address is [EMAIL', '_1]'), expander(known));
  assert.equal(textsOf(stream), 'her address is claire@example.fr');
});

test('a replacement cut by a network chunk boundary, not an event boundary', async () => {
  // The two boundaries are unrelated: a chunk can end in the middle of an event, and
  // half an event is not something to parse or to hand the page.
  const [first, second] = deltas('write to [EMAIL_1] today');
  const whole = first!;
  const stream = await through([whole.slice(0, 30), whole.slice(30)], expander(known));
  assert.equal(textsOf(stream), 'write to claire@example.fr today');
  void second;
});

test('an original carrying a quote and a newline still leaves valid JSON', async () => {
  // The rule the agent's own paths follow: an event is rewritten structurally, never
  // as raw bytes. Spliced into the payload as text, this original would close the
  // JSON string early and the page would fail to parse the event.
  const awkward = { '[NAME_1]': 'O\'Hara "Bob"\nline two \\ end' };
  const stream = await through(deltas('signed [NAME_1] regards'), expander(awkward));

  for (const line of stream.split('\n')) {
    if (!line.startsWith('data:')) continue;
    assert.doesNotThrow(() => JSON.parse(line.slice('data:'.length).trim()),
      'the rewritten event is not parseable JSON');
  }
  assert.equal(textsOf(stream), 'signed O\'Hara "Bob"\nline two \\ end regards');
});

test('the event name above the payload survives the rewrite', async () => {
  // A client dispatches on it. An event rewritten into a bare "data:" line is one the
  // page routes nowhere.
  const stream = await through(deltas('[EMAIL_1]'), expander(known));
  assert.ok(stream.includes('event: content_block_delta'));
});

test('an event carrying no generated text is passed through untouched', async () => {
  const raw = 'event: message_start\ndata: {"type":"message_start","message":{"id":"m1"}}\n\n';
  const stream = await through([raw], expander(known));
  assert.equal(stream, raw);
});

test('the [DONE] sentinel is passed through untouched', async () => {
  const raw = 'data: [DONE]\n\n';
  const stream = await through([raw], expander(known));
  assert.equal(stream, raw);
});

test('calls are serialised even when answers come back out of order', async () => {
  // Chunk n's tail prefixes chunk n+1, so two calls in flight at once is a corruption
  // rather than an optimisation. This drives them faster than the round trip and has
  // the stub answer the first call slowest — an implementation that fired them in
  // parallel would interleave the tails and lose the split replacement.
  const inner = expander(known);
  let call = 0;
  const slowFirst: Ask = async (chunk) => {
    const delay = call++ === 0 ? 25 : 0;
    const answer: UnmaskAnswer = await inner(chunk);
    await new Promise((resolve) => setTimeout(resolve, delay));
    return answer;
  };

  const stream = await through(deltas('a [EMA', 'IL_1] b [EMA', 'IL_1] c'), slowFirst);
  assert.equal(textsOf(stream), 'a claire@example.fr b claire@example.fr c');
});

test('the older completion shape is restored too', async () => {
  const raw = (text: string) =>
    `data: ${JSON.stringify({ type: 'completion', completion: text })}\n\n`;
  const stream = await through([raw('write to [EMA'), raw('IL_1]')], expander(known));
  assert.equal(textsOf(stream), 'write to claire@example.fr');
});
