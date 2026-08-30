import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

import { mask, unmask, type AgentConfig } from '../src/agent.ts';

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

const cfg: AgentConfig = {
  baseUrl: 'http://127.0.0.1:8787',
  key: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
};

/** recording is a fetch that answers one recorded exchange and keeps what it was
 * asked, so the request the client actually built can be compared to the file. */
function recording(exchange: Exchange) {
  const seen: { url?: string; init?: RequestInit } = {};
  const fetchImpl = async (url: string, init?: RequestInit): Promise<Response> => {
    seen.url = url;
    seen.init = init;
    return new Response(JSON.stringify(exchange.response), {
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
    if (exchange.route === '/mask') {
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
  const exchange = contract.exchanges[0]!;
  const { seen, fetchImpl } = recording(exchange);
  await mask(cfg, contract.session, exchange.request.texts as string[], fetchImpl);

  const headers = seen.init?.headers as Record<string, string>;
  assert.equal(headers['X-Cloakfleet-Control'], cfg.key,
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
