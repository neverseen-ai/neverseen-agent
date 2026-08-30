import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

// The real agent, built from this repository, run for the length of one test.
//
// Not a stub, and that is the point of the whole exercise: every other test here
// answers what the extension does when the agent says X. This one answers what the
// agent actually says — which detector, which catalogue, which mapping — and the two
// together are the only thing that can be called proof.
//
// It runs under a HOME of its own, so a test never reads or writes the operator's own
// control key, and on a port of its own, so it never fights an agent already running.

const BINARY = fileURLToPath(new URL('../../bin/cloakfleet', import.meta.url));

async function freePort() {
  const server = createServer();
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address();
  await new Promise((resolve) => server.close(resolve));
  return port;
}

/** startAgent runs `cloakfleet proxy` and waits until it is answering. */
export async function startAgent({ locales = 'fr' } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'cloakfleet-home-'));
  const port = await freePort();
  const baseUrl = `http://127.0.0.1:${port}`;

  const child = spawn(BINARY, ['proxy', '-l', `127.0.0.1:${port}`], {
    env: {
      ...process.env,
      HOME: home,
      CLOAKFLEET_PII_LOCALE: locales,
      CLOAKFLEET_PII_ALLOWLIST: '',
      CLOAKFLEET_BACKEND_URL: '',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const log = [];
  child.stdout.on('data', (d) => log.push(String(d)));
  child.stderr.on('data', (d) => log.push(String(d)));

  const deadline = Date.now() + 10_000;
  for (;;) {
    if (Date.now() > deadline) {
      child.kill('SIGKILL');
      throw new Error(`the agent never answered on ${baseUrl}:\n${log.join('')}`);
    }
    try {
      const resp = await fetch(baseUrl + '/healthz');
      if (resp.ok) break;
    } catch {
      /* not up yet */
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  // Read rather than written: only the agent mints this, because a key created by a
  // reader is a key the agent does not know. `cloakfleet key` reads the same file.
  const key = readFileSync(join(home, '.cloakfleet', 'control.key'), 'utf8').trim();

  return {
    baseUrl,
    key,
    home,
    log,
    async stop() {
      if (child.exitCode !== null || child.signalCode !== null) return;
      child.kill('SIGTERM');
      await new Promise((resolve) => {
        child.once('exit', resolve);
        setTimeout(() => {
          child.kill('SIGKILL');
          resolve();
        }, 5000);
      });
    },
  };
}
