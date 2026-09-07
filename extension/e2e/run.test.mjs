import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';

import puppeteer from 'puppeteer';

import { startAgent } from './agent.mjs';
import { CONVERSATION, startSite } from './site.mjs';

// The end-to-end proof: the built extension, in a real Chrome, in front of a real
// page, talking to the real agent.
//
// Everything else in this suite tests one side of a seam with the other side stubbed,
// which is how the seams stay honest. This is the one test with nothing stubbed, and
// it is the only one that can answer the question the extension exists for: does a
// value somebody typed reach the site, and does the answer come back readable.
//
// How claude.ai gets to be on this machine: Chrome is started with
// --host-resolver-rules, which maps the hostname to a local port before any network
// happens. The origin the browser sees is https://claude.ai, so the content scripts
// declared for it run exactly as they would on the real site — which is what makes
// this a test of the extension rather than of a copy of it pointed somewhere else.

const DIST = fileURLToPath(new URL('../dist', import.meta.url));
const ADDRESS = 'claire@example.fr';

let agent;
let site;
let browser;
let extensionId;

before(async () => {
  assert.ok(existsSync(DIST + '/manifest.json'),
    'run `npm run build` first: this drives the built extension, not the sources');

  agent = await startAgent();
  site = await startSite();

  browser = await puppeteer.launch({
    headless: true,
    args: [
      `--disable-extensions-except=${DIST}`,
      `--load-extension=${DIST}`,
      // claude.ai, on this machine, before any DNS happens.
      `--host-resolver-rules=MAP claude.ai 127.0.0.1:${site.port}`,
      '--ignore-certificate-errors',
      '--no-sandbox',
    ],
  });

  // The service worker is how the extension announces its own id, and waiting for it
  // is also waiting for the extension to be loaded at all.
  const worker = await browser.waitForTarget(
    (t) => t.type() === 'service_worker' && t.url().startsWith('chrome-extension://'),
    { timeout: 15_000 },
  );
  extensionId = new URL(worker.url()).host;
});

after(async () => {
  await browser?.close();
  await site?.close();
  await agent?.stop();
});

/** connect drives the options page the way a person would: paste the key, save. */
async function connect(page) {
  await page.goto(`chrome-extension://${extensionId}/options.html`, { waitUntil: 'load' });

  await page.$eval('#baseUrl', (el, value) => ((el).value = value), agent.baseUrl);
  await page.$eval('#key', (el, value) => ((el).value = value), agent.key);
  await page.click('#save');
  await page.waitForFunction(
    () => document.getElementById('headline')?.textContent?.trim() === 'Masking',
    { timeout: 10_000 },
  );
}

test('the options page reports the agent it can actually reach', async () => {
  const page = await browser.newPage();

  // Before the key: the agent is running, and the page must say so rather than
  // reporting a failure. This is the state that reads as "broken extension" when the
  // four are folded into one message.
  await page.goto(`chrome-extension://${extensionId}/options.html`, { waitUntil: 'load' });
  await page.$eval('#baseUrl', (el, value) => ((el).value = value), agent.baseUrl);
  await page.$eval('#key', (el) => ((el).value = ''));
  await page.click('#save');
  await page.waitForFunction(
    () => document.getElementById('guidance')?.textContent?.includes('neverseen key'),
    { timeout: 10_000 },
  );

  // A key of the right shape that this agent did not write: refused, and reported as
  // refused rather than as an agent that is down.
  await page.$eval('#key', (el) => ((el).value = 'f'.repeat(64)));
  await page.click('#save');
  await page.waitForFunction(
    () => document.getElementById('headline')?.textContent?.includes('refused'),
    { timeout: 10_000 },
  );

  // And the real one.
  await connect(page);
  const dot = await page.$eval('#dot', (el) => el.className);
  assert.match(dot, /full/, 'a connected, fully masking agent is not showing green');

  await page.close();
});

test('a value typed into the page never reaches the site, and comes back readable', async () => {
  const page = await browser.newPage();
  await connect(page);

  const before = site.received.length;
  await page.goto(`https://claude.ai/chat/${CONVERSATION}`, { waitUntil: 'load' });

  const outcome = await page.evaluate(
    (address) => window.send(`Envoie le dossier à ${address} aujourd'hui`),
    ADDRESS,
  );
  assert.equal(outcome, 'ok', await page.$eval('#error', (el) => el.textContent));

  // What the site received. This is the half nothing else can prove: a page that was
  // never touched renders exactly the same thing.
  assert.equal(site.received.length, before + 1, 'the site received no message at all');
  const sent = site.received[site.received.length - 1];
  assert.ok(!sent.includes(ADDRESS), `the address reached the site in clear: ${sent}`);
  assert.match(sent, /\[EMAIL_\d+\]/, `nothing was masked: ${sent}`);

  // And that the rest of the body went through untouched, so the site can still route
  // on it.
  const body = JSON.parse(sent);
  assert.equal(body.model, 'claude-fable-5');
  assert.equal(body.timezone, 'Europe/Paris');

  // What the page shows. The answer echoed the replacement back in five-character
  // events, so every one of them was split across two — the case the tail exists for.
  await page.waitForFunction(
    (address) => document.getElementById('out')?.textContent?.includes(address),
    { timeout: 10_000 },
    ADDRESS,
  );
  const rendered = await page.$eval('#out', (el) => el.textContent);
  assert.equal(rendered, `You said: Envoie le dossier à ${ADDRESS} aujourd'hui`,
    'the answer did not come back as it was typed');
  assert.ok(!rendered.includes('[EMAIL'), 'a replacement was left on screen');

  await page.close();
});

test('a value ending the message is restored by the flush', async () => {
  // The stream ends on the replacement, so its last characters are held back with
  // nothing after them to carry them. Without the final call they are simply never
  // shown, and the page reads as a sentence with its last word missing.
  const page = await browser.newPage();
  await connect(page);
  await page.goto(`https://claude.ai/chat/${CONVERSATION}`, { waitUntil: 'load' });

  await page.evaluate((address) => window.send(`écris à ${address}`), ADDRESS);
  await page.waitForFunction(
    (address) => document.getElementById('out')?.textContent?.endsWith(address),
    { timeout: 10_000 },
    ADDRESS,
  );

  const rendered = await page.$eval('#out', (el) => el.textContent);
  assert.equal(rendered, `You said: écris à ${ADDRESS}`);

  await page.close();
});

test('a credential is tokenized and restored alongside the address', async () => {
  const page = await browser.newPage();
  await connect(page);
  await page.goto(`https://claude.ai/chat/${CONVERSATION}`, { waitUntil: 'load' });

  const key = 'sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789';
  const before = site.received.length;
  await page.evaluate((secret) => window.send(`ma clé est ${secret}`), key);
  await page.waitForFunction(
    (secret) => document.getElementById('out')?.textContent?.includes(secret),
    { timeout: 10_000 },
    key,
  );

  const sent = site.received[before];
  assert.ok(!sent.includes(key), `the credential reached the site in clear: ${sent}`);
  assert.match(sent, /\[ANTHROPIC_KEY_\d+\]/,
    'a credential must take a bracket token, never a stand-in that somebody would try to use');

  await page.close();
});

test('a hostile script on the page cannot read another session back', async () => {
  // The finding this fix exists for, in a real browser.
  //
  // The interceptor lives in the page's own world, so any script the site loads can
  // post exactly what it posts and read the answer off the same channel. There is no
  // way to authenticate that world — it *is* the page. What there is a way to do is
  // stop believing it about the one field that decides whose mapping is read.
  //
  // The catastrophic case is the agent's anonymous "default" session: every tool that
  // sends no session header shares it, so on a workstation it carries every value the
  // agent has masked since it started — a terminal's traffic included. Tokens are
  // guessable, so naming that session was enough.
  const page = await browser.newPage();
  await connect(page);
  await page.goto(`https://claude.ai/chat/${CONVERSATION}`, { waitUntil: 'load' });

  // Stand in for a tool talking to the agent through the proxy: it sends no session
  // header, so it lands in "default", exactly as Claude Code does.
  const terminalAddress = 'bruno.terminal@example.fr';
  const masked = await (
    await fetch(agent.baseUrl + '/mask', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Neverseen-Control': agent.key,
        'X-Session-Id': 'default',
      },
      body: JSON.stringify({ texts: [`terminal traffic: ${terminalAddress}`] }),
    })
  ).json();

  const terminalToken = /\[EMAIL_\d+\]/.exec(masked.texts[0])?.[0];
  assert.ok(terminalToken, `nothing was masked for the terminal: ${masked.texts[0]}`);

  // Now the attack, from a script running on claude.ai.
  const stolen = await page.evaluate(
    (session, token) => window.forge({ kind: 'unmask', session, text: token, tail: '', final: true }),
    'default',
    terminalToken,
  );

  const back = JSON.stringify(stolen);
  assert.ok(!back.includes(terminalAddress),
    `a page script read the terminal's traffic back: ${back}`);
  assert.equal(stolen.ok && stolen.result.expanded, terminalToken,
    'the forged session was ignored, so the token belongs to no mapping the page may see');

  // And the same for another conversation, whose id a script on the site can obtain.
  const otherAddress = 'other.conversation@example.fr';
  const otherMasked = await (
    await fetch(agent.baseUrl + '/mask', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Neverseen-Control': agent.key,
        'X-Session-Id': 'claude:11111111-2222-3333-4444-555555555555',
      },
      body: JSON.stringify({ texts: [`another chat: ${otherAddress}`] }),
    })
  ).json();
  const otherToken = /\[EMAIL_\d+\]/.exec(otherMasked.texts[0])?.[0];

  const crossed = await page.evaluate(
    (session, token) => window.forge({ kind: 'unmask', session, text: token, tail: '', final: true }),
    'claude:11111111-2222-3333-4444-555555555555',
    otherToken,
  );
  assert.ok(!JSON.stringify(crossed).includes(otherAddress),
    'a page script read another conversation back');

  await page.close();
});

test('what the fix does not close, held here so nobody thinks it did', async () => {
  // The residual, asserted rather than described. A hostile script on the site can
  // still ask about the conversation the tab is actually showing — whose values the
  // page is already being handed, to render them. Closing this would mean
  // authenticating the page's own world, which cannot be done: any secret placed
  // there to prove "this is really the interceptor" is readable by the page.
  //
  // It is written as a passing test so that a future change which *does* close it
  // fails here and is noticed, rather than being mistaken for a regression.
  const page = await browser.newPage();
  await connect(page);
  await page.goto(`https://claude.ai/chat/${CONVERSATION}`, { waitUntil: 'load' });

  const mine = 'mine.own@example.fr';
  await page.evaluate((address) => window.send(`écris à ${address}`), mine);
  await page.waitForFunction(
    (address) => document.getElementById('out')?.textContent?.includes(address),
    { timeout: 10_000 },
    mine,
  );

  const own = await page.evaluate(() =>
    window.forge({ kind: 'unmask', text: '[EMAIL_1]', tail: '', final: true }),
  );
  assert.ok(own.ok, `the relay refused an ask for this tab's own conversation: ${JSON.stringify(own)}`);

  await page.close();
});

test('with the agent stopped, the send is blocked rather than forwarded', async () => {
  // Fail closed, in the browser, for real. This is the rule somebody will be tempted
  // to soften: a blocked send looks like a broken site, and a forwarded one looks like
  // nothing at all until somebody reads the provider's logs.
  const page = await browser.newPage();
  await connect(page);
  await page.goto(`https://claude.ai/chat/${CONVERSATION}`, { waitUntil: 'load' });

  await agent.stop();

  const before = site.received.length;
  const outcome = await page.evaluate(
    (address) => window.send(`et aussi ${address}`),
    ADDRESS,
  );

  assert.equal(outcome, 'blocked', 'the send went through with no agent to mask it');
  assert.equal(site.received.length, before,
    'the message reached the site while nothing was masking it');

  // And the person is told why, with the command that fixes it.
  await page.waitForFunction(() => document.getElementById('neverseen-banner') !== null, {
    timeout: 10_000,
  });
  const banner = await page.$eval('#neverseen-banner', (el) => el.textContent);
  assert.match(banner, /not sent/);
  assert.match(banner, /neverseen proxy/);

  await page.close();
});
