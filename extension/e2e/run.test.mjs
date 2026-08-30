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
    () => document.getElementById('guidance')?.textContent?.includes('cloakfleet key'),
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
  await page.waitForFunction(() => document.getElementById('cloakfleet-banner') !== null, {
    timeout: 10_000,
  });
  const banner = await page.$eval('#cloakfleet-banner', (el) => el.textContent);
  assert.match(banner, /not sent/);
  assert.match(banner, /cloakfleet proxy/);

  await page.close();
});
