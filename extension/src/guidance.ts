import { type Health, levelOf, type Note, type Reason } from './protocol.ts';

// What the options page says, decided apart from the page that draws it.
//
// Separate for the reason internal/tray keeps render and watch out of the toolkit:
// everything here is a decision about what somebody is told, and a decision buried in
// DOM calls is one nothing can test. The page below is then a few lines that put
// strings into elements.
//
// The four failures are kept apart deliberately. Folded into one "it is not working"
// they all read as a broken extension, and the commonest of them — the agent is
// simply not running — is a thing the person fixes in one command. This is the
// shellTools discipline transposed: the line to type travels to the place it is
// needed, rather than living in a manual.

export type Outcome<T> =
  | { ok: true; value: T }
  | { ok: false; reason: Reason; message: string };

export type Diagnosis = {
  /** level drives the indicator, and follows the agent's Level rather than "is it
   * masking": an agent with a category switched off is masking, and a green light
   * over that is a green light over the values that are not being replaced. */
  level: 'none' | 'partial' | 'full';

  headline: string;
  body: string;

  /** command is a line to copy, when there is one that fixes this state. */
  command?: string;

  /** details are the specifics under the sentence — the categories in clear, say. */
  details?: string[];
};

/**
 * diagnose says what state the connection is in.
 *
 * Two inputs because the two questions are genuinely different, and telling them
 * apart is the whole point: /healthz is unauthenticated, so it answers "is the agent
 * there", and the authenticated probe answers "does it accept this key". Asked with
 * one call they would collapse into a single failure, and a wrong key would report as
 * an agent that is down — sending somebody to restart a service that is running.
 */
export function diagnose(
  agent: Outcome<Health>,
  probe: Outcome<null> | null,
  baseUrl: string,
): Diagnosis {
  if (!agent.ok) {
    return {
      level: 'none',
      headline: 'Cloakfleet is not running',
      body:
        `Nothing answered at ${baseUrl}. Your web chats reach the model directly, ` +
        'unmasked — which is deliberate: a stopped agent leaves your tools working ' +
        'rather than broken. Nothing is sent through this extension while it is down.',
      command: 'cloakfleet proxy',
    };
  }

  if (probe === null) {
    return {
      level: 'none',
      headline: 'One command to connect',
      body:
        'The agent is running. It needs the control key before this extension may ' +
        'ask it to mask anything. Print it and paste it below.',
      command: 'cloakfleet key',
    };
  }

  if (!probe.ok) {
    if (probe.reason === 'unauthorised') {
      return {
        level: 'none',
        headline: 'The key was refused',
        body:
          'The agent is running and does not accept the key stored here — it was ' +
          'rotated, or it belongs to a different agent. Print the current one and ' +
          'paste it below.',
        command: 'cloakfleet key',
      };
    }
    return {
      level: 'none',
      headline: 'The agent refused',
      // Its own words: they say what it refused and why, and replacing them with
      // "the request failed" throws away the only part somebody can act on.
      body: probe.message,
    };
  }

  const health = agent.value;
  const level = levelOf(health);

  if (level === 'none') {
    return {
      level,
      headline: 'Connected, but masking almost nothing',
      body:
        'The agent is answering and this extension can reach it, but no country ' +
        'pattern set is loaded — so only the locale-independent identifiers and the ' +
        'credentials are recognised at all.',
      command: `cloakfleet mask --locales ${(health.available_locales ?? ['fr']).join(',')}`,
    };
  }

  const off = inClear(health);
  if (level === 'partial') {
    return {
      level,
      headline: `Masking, with ${off.length} categor${off.length === 1 ? 'y' : 'ies'} in clear`,
      body:
        'Everything else is being replaced. What is listed below leaves this machine ' +
        'as it was typed.',
      // Named, not counted. A count sends somebody looking; the names tell them
      // whether the one they care about is among them.
      details: off,
      command: 'cloakfleet mask',
    };
  }

  return {
    level,
    headline: 'Masking',
    body: `Every category ${health.locales.join(', ')} loaded is being replaced, ${
      health.substitution === 'fake' ? 'with stand-ins' : 'with tokens'
    }.`,
  };
}

/**
 * blockedMessage is what the page says when a send did not happen.
 *
 * Here rather than in the interceptor, with the rest of what somebody is told, and
 * for the same reason: the commonest of these is an agent that is simply not running,
 * and the person fixes it in one command. A banner that said "an error occurred"
 * would have them retyping their message or blaming the site.
 */
export function blockedMessage(reason: Reason, detail = ''): string {
  switch (reason) {
    case 'unreachable':
      return (
        'Cloakfleet: your message was not sent, because the agent on this machine is ' +
        'not answering and nothing would have masked it. Start it with `cloakfleet proxy`.'
      );
    case 'unconfigured':
      return (
        'Cloakfleet: your message was not sent, because this extension has no control ' +
        'key yet. Run `cloakfleet key` and paste it into the extension’s options.'
      );
    case 'unauthorised':
      return (
        'Cloakfleet: your message was not sent, because the agent refused this ' +
        'extension’s key. Run `cloakfleet key` and paste the current one into its options.'
      );
    default:
      // The agent's own words when there are any. There are none when this is
      // rendering a banner: the relay writes those, from the situation alone, because
      // a sentence supplied by the page is arbitrary text behind this extension's
      // name. The options page is where the agent's answer can be read in full.
      return detail
        ? 'Cloakfleet: your message was not sent — ' + detail
        : 'Cloakfleet: your message was not sent, because the agent refused to mask it. ' +
          'The extension’s options page says what it answered.';
  }
}

/**
 * noteMessage is what a banner says, chosen from a closed set of situations.
 *
 * The set is closed on purpose. A note crossing from the page's world carries which
 * situation it was and never the sentence, so that a script on the site cannot put
 * words of its own behind this extension's name — see bridge.ts.
 */
export function noteMessage(note: Note, reason: Reason): string {
  switch (note) {
    case 'restore':
      // Deliberately not a blocked send: nothing was lost and nothing leaked. Saying
      // "your message was not sent" here would send somebody looking for a message
      // that did in fact arrive.
      return (
        'Cloakfleet: the answer below could not be turned back into your own values, ' +
        'so it shows the replacements instead. Nothing was lost, and nothing left this machine.'
      );
    case 'transport':
      return (
        'Cloakfleet: this page tried to send over a transport that cannot be masked, ' +
        'so it was blocked. Nothing left this machine in clear.'
      );
    default:
      return blockedMessage(reason);
  }
}

function inClear(health: Health): string[] {
  const out: string[] = [];
  for (const group of health.groups ?? []) {
    for (const category of group.categories) {
      if (category.off) out.push(category.label);
    }
  }
  return out;
}
