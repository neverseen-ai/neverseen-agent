import { DEFAULT_BASE_URL, health, unmask } from './agent.ts';
import { AgentError } from './agent.ts';
import { diagnose, type Outcome } from './guidance.ts';
import { type Health } from './protocol.ts';
import { load, looksLikeAKey, save, type Storage } from './settings.ts';

// The options page: a status the person can read and one field to fill.
//
// It talks to the agent directly rather than through the service worker, because it
// is already extension code with the same host permissions and the worker adds a hop
// that could only fail. It is the one surface here that may hold the key, and the one
// place it is ever displayed — as what somebody just typed, never read back from
// storage into a page the browser might autofill elsewhere.

const storage = chrome.storage.local as unknown as Storage;

const el = {
  dot: byId('dot'),
  headline: byId('headline'),
  guidance: byId('guidance'),
  key: byId('key') as HTMLInputElement,
  baseUrl: byId('baseUrl') as HTMLInputElement,
  save: byId('save') as HTMLButtonElement,
  saved: byId('saved'),
};

function byId(id: string): HTMLElement {
  const node = document.getElementById(id);
  if (!node) throw new Error(`the options page has no #${id}`);
  return node;
}

/** refresh asks both questions and redraws from the answers. */
async function refresh(): Promise<void> {
  const settings = await load(storage);
  el.baseUrl.placeholder = DEFAULT_BASE_URL;
  if (settings.baseUrl !== DEFAULT_BASE_URL) el.baseUrl.value = settings.baseUrl;

  const agent: Outcome<Health> = await attempt(() => health(settings.baseUrl, fetch));

  // Only asked when there is a key to ask with. Probing without one would report
  // "unauthorised" at somebody who has simply not pasted it yet, which is the state
  // that has its own message.
  const probe: Outcome<null> | null = !settings.key
    ? null
    : await attempt(async () => {
        // /unmask with nothing in it: it exercises the key and the loopback rule and
        // asks the agent to do no work at all. /mask would have been the obvious
        // probe and it counts a request — a fleet view would show this page's every
        // open as traffic.
        await unmask(settings, 'neverseen:probe', { text: '', tail: '', final: true }, fetch);
        return null;
      });

  draw(diagnose(agent, agent.ok ? probe : null, settings.baseUrl));
}

function draw(d: ReturnType<typeof diagnose>): void {
  el.dot.className = 'dot ' + d.level;
  el.headline.textContent = d.headline;

  el.guidance.replaceChildren();

  const body = document.createElement('p');
  body.textContent = d.body;
  el.guidance.append(body);

  if (d.details?.length) {
    const list = document.createElement('ul');
    for (const item of d.details) {
      const li = document.createElement('li');
      li.textContent = item;
      list.append(li);
    }
    el.guidance.append(list);
  }

  if (d.command) el.guidance.append(commandBlock(d.command));
}

/** commandBlock is the line to type, with a button that copies it.
 *
 * The line travels to where it is needed rather than living in a manual — the same
 * rule shellTools follows for the variable that points a tool at the agent. */
function commandBlock(command: string): HTMLElement {
  const pre = document.createElement('pre');
  const code = document.createElement('code');
  code.textContent = command;

  const copy = document.createElement('button');
  copy.type = 'button';
  copy.textContent = 'Copy';
  copy.addEventListener('click', () => {
    void navigator.clipboard.writeText(command);
    copy.textContent = 'Copied';
    setTimeout(() => (copy.textContent = 'Copy'), 1200);
  });

  pre.append(code, copy);
  return pre;
}

el.save.addEventListener('click', () => {
  void (async () => {
    const key = el.key.value.trim();
    if (key && !looksLikeAKey(key)) {
      // Said here rather than left to the agent's 403, which reads as "the agent
      // refused you" when what happened is that a shell prompt was pasted along with
      // the key.
      el.saved.textContent = 'That is not a control key: 64 hexadecimal characters, nothing else.';
      return;
    }

    await save(storage, { baseUrl: el.baseUrl.value || DEFAULT_BASE_URL, key });
    // Cleared rather than left on screen: the field is for pasting, not for storing a
    // secret in a page the browser may restore.
    el.key.value = '';
    el.saved.textContent = 'Saved.';
    await refresh();
  })();
});

async function attempt<T>(run: () => Promise<T>): Promise<Outcome<T>> {
  try {
    return { ok: true, value: await run() };
  } catch (err) {
    if (err instanceof AgentError) return { ok: false, reason: err.reason, message: err.message };
    return { ok: false, reason: 'refused', message: err instanceof Error ? err.message : String(err) };
  }
}

void refresh();
