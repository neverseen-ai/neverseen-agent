import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

import { DEFAULT_BASE_URL, mask, proofOf, prove, unmask, type AgentConfig } from '../src/agent.ts';

// The extension's half of the contract with the agent.
//
// The same file internal/proxy/contract_test.go replays against a real agent. That
// one proves the agent answers these responses; this one proves the client sends
// these requests and reads those answers. Neither is enough alone, and together they
// are what the same-repository decision was made for: a field renamed on either side
// fails on the other, in the commit that renamed it.
//
// Nothing here is generated. The requests and the reasons in the file are written by
// hand; only the responses are recorded, and only by `make contract-update`.

type Exchange = {
  name: string;
  why: string;
  // The agent's mode from this exchange on. Nothing for the client to do with it:
  // the same calls in either mode is the property the fake-mode exchanges hold.
  substitution?: string;
  route: string;
  request: Record<string, unknown>;
  response: Record<string, unknown>;
};

type Contract = {
  detector: { locales: string[]; substitution: string; secret_level: string };
  session: string;
  exchanges: Exchange[];
};

const contract = JSON.parse(
  readFileSync(fileURLToPath(new URL('../testdata/contract.json', import.meta.url)), 'utf8'),
) as Contract;

// DEFAULT_BASE_URL because the recorded proof is bound to the port the client dials, and
// the agent's half replays /prove as reached on proxy.DefaultListen.
const cfg: AgentConfig = {
  baseUrl: DEFAULT_BASE_URL,
  key: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
};

/** recording is a fetch that answers one recorded exchange and keeps what it was
 * asked, so the request the client actually built can be compared to the file. The
 * proof every call makes first is answered as the agent would answer it — the /prove
 * exchange is where the two computations are held to each other. */
function recording(exchange: Exchange) {
  const seen: { url?: string; init?: RequestInit } = {};
  const fetchImpl = async (url: string, init?: RequestInit): Promise<Response> => {
    let response = exchange.response;
    if (url.endsWith('/prove') && exchange.route !== '/prove') {
      const { nonce } = JSON.parse(init?.body as string) as { nonce: string };
      response = { proof: await proofOf(cfg.key, nonce, '9787') };
    } else {
      seen.url = url;
      seen.init = init;
    }
    return new Response(JSON.stringify(response), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  };
  return { seen, fetchImpl };
}

for (const exchange of contract.exchanges) {
  test(`contract: ${exchange.name}`, async () => {
    const { seen, fetchImpl } = recording(exchange);

    let result: unknown;
    if (exchange.route === '/prove') {
      // The nonce from the file, so the recorded proof is the answer: accepted only if
      // this client computes what the agent computed.
      await prove(cfg, fetchImpl, exchange.request.nonce as string);
      result = exchange.response;
    } else if (exchange.route === '/mask') {
      result = await mask(cfg, contract.session, exchange.request.texts as string[], fetchImpl);
    } else if (exchange.route === '/unmask') {
      result = await unmask(
        cfg,
        contract.session,
        {
          text: exchange.request.text as string,
          tail: exchange.request.tail as string,
          final: exchange.request.final as boolean,
        },
        fetchImpl,
      );
    } else {
      throw new Error(`the contract names a route this client has no call for: ${exchange.route}`);
    }

    assert.equal(seen.url, cfg.baseUrl + exchange.route, exchange.why);
    assert.equal(seen.init?.method, 'POST');

    // The request the client built, against the one in the file. This is the half
    // that catches a client which quietly stopped sending the tail: it would still
    // parse every recorded answer and restore nothing in a real stream.
    assert.deepEqual(JSON.parse(seen.init?.body as string), exchange.request, exchange.why);

    // And the answer, parsed into what the caller gets.
    assert.deepEqual(result, exchange.response, exchange.why);
  });
}

test('contract: every request carries the key and the session', async () => {
  const exchange = contract.exchanges.find((e) => e.route === '/mask')!;
  const { seen, fetchImpl } = recording(exchange);
  await mask(cfg, contract.session, exchange.request.texts as string[], fetchImpl);

  const headers = seen.init?.headers as Record<string, string>;
  assert.equal(headers['X-Neverseen-Control'], cfg.key,
    'without the control header the agent refuses, and the send is blocked');
  assert.equal(headers['X-Session-Id'], contract.session,
    '/mask and /unmask for one conversation must name the same session, or the expansion finds nothing');
});

test('contract: the session is one name for both routes', () => {
  // Held here rather than left to a reader noticing: the file has one session for
  // every exchange because that is the property, not a convenience of the fixture.
  assert.ok(contract.session.length > 0);
  assert.ok(
    contract.exchanges.length >= 2,
    'a contract with one exchange cannot show a mapping being carried across calls',
  );
});

// The listener is asked to prove itself before anything is sent, and one that cannot is
// sent nothing: not the key, not a text. Asserted on every call it received.
test('contract: a listener that cannot prove it holds the key is sent nothing', async () => {
  const exchange = contract.exchanges.find((e) => e.route === '/mask')!;
  for (const answer of [{ status: 200, body: { proof: 'f'.repeat(64) } }, { status: 200, body: {} }, { status: 404, body: {} }]) {
    const calls: { url: string; init?: RequestInit }[] = [];
    const fetchImpl = async (url: string, init?: RequestInit): Promise<Response> => {
      calls.push({ url, init });
      return new Response(JSON.stringify(answer.body), { status: answer.status });
    };

    await assert.rejects(mask(cfg, contract.session, exchange.request.texts as string[], fetchImpl));
    assert.deepEqual(calls.map((c) => c.url), [cfg.baseUrl + '/prove'], 'something was sent after a failed proof');
    const sent = JSON.stringify(calls[0]!.init);
    assert.ok(!sent.includes(cfg.key), 'the key went out with the challenge');
    for (const text of exchange.request.texts as string[]) {
      assert.ok(!sent.includes(text), 'a text went out with the challenge');
    }
  }
});

// A streamed answer is restored an event at a time, and a proof before each call was a
// second round trip for every event. A proof stands for the calls right after it, and a
// failed send drops it: the next call proves the listener again.
test('contract: a proof stands for the calls right after it, until a send fails', async () => {
  const exchange = contract.exchanges.find((e) => e.route === '/mask')!;
  const texts = exchange.request.texts as string[];
  const urls: string[] = [];
  let status = 200;
  const fetchImpl = async (url: string, init?: RequestInit): Promise<Response> => {
    urls.push(url);
    if (url.endsWith('/prove')) {
      const { nonce } = JSON.parse(init?.body as string) as { nonce: string };
      return new Response(JSON.stringify({ proof: await proofOf(cfg.key, nonce, '9787') }), { status: 200 });
    }
    return new Response(JSON.stringify(exchange.response), { status });
  };
  const proofs = () => urls.filter((u) => u.endsWith('/prove')).length;

  await mask(cfg, contract.session, texts, fetchImpl);
  await mask(cfg, contract.session, texts, fetchImpl);
  assert.equal(proofs(), 1, 'a second call so soon after the first proved the listener again');

  status = 403; // the key refused: the proof no longer says anything
  await assert.rejects(mask(cfg, contract.session, texts, fetchImpl));
  status = 200;
  await mask(cfg, contract.session, texts, fetchImpl);
  assert.equal(proofs(), 2, 'a call after a failed send was not proved again');
});
