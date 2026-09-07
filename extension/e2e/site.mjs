import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync } from 'node:fs';
import { createServer } from 'node:https';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// A stand-in for claude.ai, served over HTTPS on this machine.
//
// It has to be HTTPS on that hostname, and not a convenience: the extension's content
// scripts are declared for `https://claude.ai/*`, and an origin the manifest does not
// name is one they never run in. Chrome is pointed here with --host-resolver-rules,
// so what the browser sees — the origin, the content scripts that apply, the cookie
// jar — is exactly what it would see on the real site.
//
// What it is not is a model. It echoes back what it received, in pieces small enough
// to cut a replacement in half, which is the case the tail exists for and the one a
// real provider produces by accident.

/** DELTA_SIZE is how much text each event carries.
 *
 * Small on purpose. A bracket token is a dozen characters, so five-character events
 * guarantee that every replacement in the answer is split across two of them — the
 * case that is rare in the wild, impossible to arrange on demand, and the whole
 * reason this protocol carries a tail. */
const DELTA_SIZE = 5;

export const CONVERSATION = '9f1c0d2e-4b6a-4f31-8a5e-2c7d1e0b3a44';

/** selfSigned mints a certificate for claude.ai. Chrome is told to ignore
 * certificate errors, so this only has to exist. */
function selfSigned() {
  const dir = mkdtempSync(join(tmpdir(), 'neverseen-e2e-'));
  const key = join(dir, 'key.pem');
  const cert = join(dir, 'cert.pem');
  execFileSync('openssl', [
    'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
    '-keyout', key, '-out', cert, '-days', '1',
    '-subj', '/CN=claude.ai',
    '-addext', 'subjectAltName=DNS:claude.ai',
  ], { stdio: 'ignore' });
  return { key: readFileSync(key), cert: readFileSync(cert) };
}

/**
 * startSite serves the page and the completion endpoint, and records every body it
 * was sent.
 *
 * The recording is half the proof. What the page renders says the restoration
 * happened; what arrived here says the masking did — and only the second one can tell
 * a working extension from a page that was never touched.
 */
export async function startSite() {
  const { key, cert } = selfSigned();
  const received = [];

  const server = createServer({ key, cert }, (req, res) => {
    const url = new URL(req.url, 'https://claude.ai');

    if (url.pathname.endsWith('/completion')) {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        received.push(body);

        let prompt = '';
        try {
          prompt = JSON.parse(body).prompt ?? '';
        } catch {
          prompt = body;
        }

        res.writeHead(200, {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        });

        // Echoed back, which is what makes this a round trip: whatever the extension
        // masked on the way out comes back as a replacement, and the page must show
        // the original.
        const answer = `You said: ${prompt}`;
        for (let i = 0; i < answer.length; i += DELTA_SIZE) {
          const piece = answer.slice(i, i + DELTA_SIZE);
          res.write(
            'event: content_block_delta\n' +
              `data: ${JSON.stringify({
                type: 'content_block_delta',
                delta: { type: 'text_delta', text: piece },
              })}\n\n`,
          );
        }
        res.write('event: message_stop\ndata: {"type":"message_stop"}\n\n');
        res.end();
      });
      return;
    }

    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end(PAGE);
  });

  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return {
    port: server.address().port,
    received,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

/**
 * PAGE is the chat, reduced to what matters: a fetch and a rendered stream.
 *
 * Deliberately calls window.fetch from an ordinary inline script, which is what the
 * real site does and what the interceptor has to be in front of. If it captured fetch
 * into a local at parse time this would still work — and that is exactly the case
 * document_start ordering exists to cover, so the script does it the ordinary way and
 * the ordering is what is being tested.
 */
const PAGE = `<!doctype html>
<meta charset="utf-8">
<title>Chat</title>
<body>
<pre id="out"></pre>
<pre id="error"></pre>
<script>
  const conversation = location.pathname.split('/').pop();

  // A script standing in for a hostile one on the site: it posts what the interceptor
  // posts, on the same channel, and reads the answer. Nothing about this is exotic —
  // it is what any script the page loads can do, which is precisely why the relay
  // must not believe what such a message says about a session.
  window.forge = (ask) =>
    new Promise((resolve) => {
      const id = 900000 + Math.floor(Math.random() * 10000);
      const onMessage = (event) => {
        if (event.source !== window) return;
        const message = event.data;
        if (!message || message.source !== 'neverseen:relay' || message.id !== id) return;
        window.removeEventListener('message', onMessage);
        resolve(message.reply);
      };
      window.addEventListener('message', onMessage);
      window.postMessage({ source: 'neverseen:page', id, ask }, '*');
      setTimeout(() => {
        window.removeEventListener('message', onMessage);
        resolve({ ok: false, reason: 'timeout', message: 'no answer' });
      }, 5000);
    });
  window.send = async (prompt) => {
    document.getElementById('out').textContent = '';
    document.getElementById('error').textContent = '';
    try {
      const resp = await fetch(
        '/api/organizations/org-1/chat_conversations/' + conversation + '/completion',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ prompt, model: 'claude-fable-5', timezone: 'Europe/Paris' }),
        },
      );
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffered = '';
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffered += decoder.decode(value, { stream: true });
        let cut;
        while ((cut = buffered.indexOf('\\n\\n')) !== -1) {
          const raw = buffered.slice(0, cut);
          buffered = buffered.slice(cut + 2);
          for (const line of raw.split('\\n')) {
            if (!line.startsWith('data:')) continue;
            try {
              const event = JSON.parse(line.slice(5).trim());
              if (event.delta && typeof event.delta.text === 'string') {
                document.getElementById('out').textContent += event.delta.text;
              }
            } catch {}
          }
        }
      }
      return 'ok';
    } catch (err) {
      document.getElementById('error').textContent = String(err && err.message || err);
      return 'blocked';
    }
  };
</script>
</body>`;
