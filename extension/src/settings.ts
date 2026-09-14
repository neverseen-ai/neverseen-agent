import { DEFAULT_BASE_URL } from './agent.ts';

// What this extension keeps, and where.
//
// chrome.storage.local: readable by this extension in this browser profile and by
// nothing else — not by the page, not by another extension, not by another account on
// the machine. It is the profile's equivalent of the 0600 the agent gives the key
// file it reads this from.
//
// Two settings and no more. The key, which is pasted; and the agent's address, which
// is discovered rather than asked for — the manual setting exists for the one case
// the discovery cannot cover, an agent moved by NEVERSEEN_LISTEN.

export type Settings = {
  /** baseUrl is where the agent answers, with no trailing slash. */
  baseUrl: string;

  /** key is the agent's control secret, as `neverseen key` prints it. */
  key: string;
};

const STORAGE_KEY = 'neverseen';

/** Storage is the slice of chrome.storage.local this needs, so a test can pass a map. */
export type Storage = {
  get(keys: string): Promise<Record<string, unknown>>;
  set(items: Record<string, unknown>): Promise<void>;
};

export async function load(storage: Storage): Promise<Settings> {
  const stored = (await storage.get(STORAGE_KEY))[STORAGE_KEY] as Partial<Settings> | undefined;
  return {
    baseUrl: normalise(stored?.baseUrl) || DEFAULT_BASE_URL,
    key: (stored?.key ?? '').trim(),
  };
}

export async function save(storage: Storage, settings: Settings): Promise<void> {
  await storage.set({
    [STORAGE_KEY]: {
      baseUrl: normalise(settings.baseUrl) || DEFAULT_BASE_URL,
      key: settings.key.trim(),
    },
  });
}

/**
 * looksLikeAKey reports whether a pasted string is the shape the agent writes.
 *
 * Checked here as well as by the agent, and the reason is the message: a person who
 * pasted a line of the terminal's output around the key learns it from the page they
 * are looking at rather than from a 403 that reads as "the agent refused you".
 * proxy.trimKey applies the same rule for the same reason — a truncated secret is
 * worse than none, because it looks like protection.
 */
export function looksLikeAKey(value: string): boolean {
  return /^[0-9a-f]{64}$/.test(value.trim());
}

/**
 * ACCEPTED_HOSTS are the hostnames a base URL may name, and the list is what the
 * manifest's host_permissions cover — `http://127.0.0.1/*` and nothing else.
 *
 * Two reasons, and either alone would do. The key travels in a header on every
 * /mask and /unmask call, so a base URL is where the control secret is sent — a
 * pasted host anywhere on the internet would receive it. And the worker cannot reach
 * any other host anyway: `localhost` and `[::1]` are loopback too, but a host the
 * manifest does not name is one every fetch to it fails on, which reads as "the
 * agent is not running" at somebody whose agent is.
 */
const ACCEPTED_HOSTS: readonly string[] = ['127.0.0.1'];

/**
 * baseUrlProblem says why a base URL cannot be used, or null when it can. An empty
 * value is fine: it means the default.
 */
export function baseUrlProblem(value: string): string | null {
  const trimmed = value.trim();
  if (trimmed === '') return null;

  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return 'That is not an address.';
  }
  // The port is checked as well as named, and it is the check rather than the
  // message that was wrong: `http://127.0.0.1` parses, names an accepted host, and
  // was accepted — so the key then went to port 80, which is the one thing this
  // function exists to prevent, while the sentence the person read said "with a
  // port". A URL reports no port for the scheme's default, so an explicit `:80` is
  // refused here too; that is the safe direction, since nothing puts this agent
  // there.
  if (url.protocol !== 'http:' || !ACCEPTED_HOSTS.includes(url.hostname) || url.port === '') {
    return (
      'The agent address must be http://127.0.0.1 with a port. The control key is ' +
      'sent to whatever answers there, and this extension may reach nothing else — ' +
      'not localhost, not [::1].'
    );
  }
  return null;
}

/** normalise trims the address and drops a trailing slash, and returns '' for one
 * that cannot be used, so the caller falls back to the default rather than sending
 * the key to it. */
function normalise(url: string | undefined): string {
  const trimmed = (url ?? '').trim().replace(/\/+$/, '');
  return baseUrlProblem(trimmed) === null ? trimmed : '';
}
