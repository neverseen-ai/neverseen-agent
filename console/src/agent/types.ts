// The shapes the console draws, named after the agent's own types.
//
// Health, HealthGroup and HealthCategory mirror internal/proxy/status.go field for
// field — the JSON names are the ones /healthz serves. The day's count is not on
// /healthz, deliberately: that route is unauthenticated and carries what the agent
// applies, never what somebody did with it, so it comes from GET /today behind the
// control key.

export type MaskingLevel = "full" | "partial" | "none";

/** internal/proxy/status.go HealthCategory: one switch. */
export interface HealthCategory {
  code: string;
  label: string;
  /** Currently not masked. */
  off?: boolean;
  /** May not be switched off at all (a credential, or a declared value). */
  locked?: boolean;
  /** What the loaded patterns recognise it by — what it looks like, not what it is. */
  notations?: string[];
}

/** internal/proxy/status.go HealthGroup: one family. */
export interface HealthGroup {
  code: string;
  label: string;
  categories: HealthCategory[];
  /** Filed under Credentials rather than Personal data — the agent's answer, never the surface's. */
  credentials?: boolean;
}

/** How a tool is pointed at this agent for one provider — shellenv.go shellTools. */
export interface HealthTool {
  provider: string;
  /** The variable that provider's SDKs read. */
  variable: string;
  /** The command people run against that variable, where there is one everybody means. */
  cli?: string;
  /** What somebody has to know before trusting the line. Travels with it everywhere. */
  caveat?: string;
}

/** internal/proxy/status.go Health: what /healthz answers. */
export interface Health {
  status: string;
  version: string;
  locales: string[];
  substitution: string;
  secret_level: string;
  providers: string[];
  masking: MaskingLevel;
  substitutions?: string[];
  secret_levels?: string[];
  available_locales?: string[];
  /** Labels for a reader, never codes to send back. The lists overlap: never sum them. */
  locale_categories?: Record<string, string[]>;
  /** The catalogue's families and their categories, as the agent files them. */
  groups?: HealthGroup[];
  /** The whole switched-off intent as codes, including what no loaded locale can emit. */
  off?: string[];
  /** How a tool is pointed at this agent, for the providers that have a variable. */
  tools?: HealthTool[];
}

/** What PUT /policy carries: the whole state, never a patch. */
export interface Policy {
  off: string[];
  locales: string[];
  substitution: string;
  secret_level: string;
}

/** Today's tallies, from GET /today behind the control key. */
export interface Today {
  /**
   * Values hidden since local midnight, counted once per session — what was minted,
   * not every replacement. The figure the menu bar shows. Undefined when the
   * agent did not say (no key, an older build): a surface leaves it out rather than
   * drawing a zero nobody measured.
   */
  count?: number;
  /** The whole day per family code, the uncounted ones included. */
  by_family?: Record<string, number>;
  /** The same day per category code, every family included: one bar a category. */
  by_category?: Record<string, number>;
  /** The same day by local hour, 0 to 23, every family included. */
  hourly?: number[];
  /** The family codes `count` leaves out: the person's choice, applied by the agent. */
  uncounted?: string[];
  traffic?: Traffic;
  agent?: AgentState;
  /** Each model's day, in the order the models were first seen; the last may be "other". */
  tokens?: ModelTokens[];
}

/** One model's day (internal/proxy/today.go modelTokens): the four counts a provider
 * reports, summed, and their sum by local hour. Raw counts, never a cost. */
export interface ModelTokens {
  model: string;
  day: { input: number; output: number; cache_write?: number; cache_read?: number };
  hourly: number[];
}

/** What crossed the agent today (internal/proxy/today.go dayTraffic). */
export interface Traffic {
  requests: number;
  refused: number;
  upstream: { ok?: number; rate_limited?: number; rejected?: number; failed?: number; unreachable?: number };
  tools: { calls: number; restored: number };
  /** Requests per client family, from a closed vocabulary. */
  clients?: Record<string, number>;
}

/** How the agent runs and how it is exposed — the heartbeat State's own fields. */
export interface AgentState {
  started_at: string;
  /** `-a`: every value masked or put back is printed as it is. */
  console: boolean;
  /** `-v`: every exchange is written to traces/. */
  tracing: boolean;
  /** Listening beyond the loopback interface. */
  exposed: boolean;
  /** Provider codes routed somewhere other than the vendor's own host. */
  rerouted?: string[];
  /** How many values this deployment never masks. A count, never the values. */
  allowlisted?: number;
  /** The extension's last call to /mask, absent when none came since the start. */
  extension_seen_at?: string;
  /** Absent from an agent built before the page could enrol it. */
  supervision?: Supervision;
  /** A binary called rtk is on this workstation: the rtk screen asks it for its record
   * rather than making the case for it. */
  rtk?: boolean;
}

/** One day as rtk recorded it (internal/proxy/rtk.go rtkDayReply): a UTC date, and the
 * tokens the commands' output would have cost before rtk shortened it and after. */
export interface RtkDay {
  date: string;
  commands: number;
  input: number;
  output: number;
}

/** GET /rtk (internal/proxy/rtk.go rtkReply). */
export interface RtkReading {
  installed: boolean;
  path?: string;
  read_at?: string;
  /** Why rtk gave no reading, in its own words; `days` is then absent. */
  problem?: string;
  days?: RtkDay[];
}

/** Where this agent reports and how that goes (internal/proxy/supervision.go supervisionState). */
export interface Supervision {
  /** Where the agent reports, absent while it reports nowhere. */
  backend?: string;
  /** The address the enrolment form starts from: the agent's, never the page's. */
  offered: string;
  /** The identity a backend issued. Absent means not enrolled. */
  agent_id?: string;
  /** The last report the backend took. */
  delivered_at?: string;
  /** Why the last attempt failed, absent once one succeeds. */
  problem?: string;
  /** When the identity was written to this workstation. */
  enrolled_at?: string;
  /** When the next delivery is attempted. */
  next_at?: string;
  /** How many reports wait to be delivered, absent with none. */
  queued?: number;
  /** Where the first report waiting starts. */
  oldest_queued?: string;
}

export interface Snapshot {
  /** Whether anything answered at all — Status.Answering. */
  answering: boolean;
  /** Where this agent listens: the page is served by it, so its own host. */
  addr: string;
  health: Health;
  today: Today;
  /**
   * The page was opened without its one-time link and holds no control key: it
   * draws, and nothing on it can change the agent (settings.go says why).
   */
  locked: boolean;
}

/** One value the detector found — POST /test's findings (internal/proxy/testjson.go). */
export interface Finding {
  code: string;
  /** The category's name. */
  label: string;
  /** The pattern's name: what the value looked like. */
  notation: string;
  value: string;
  confidence: number;
  credential: boolean;
  /** Keeps a bracket token under stand-ins too: a credential, or no stand-in for it. */
  token_in_fake_mode: boolean;
}

export interface Rendering {
  text: string;
  /** Spans in `text` that are replacements, for highlighting. */
  marks: { start: number; end: number; credential: boolean }[];
  count: number;
}

export interface TestResult {
  findings: Finding[];
  token: Rendering;
  fake: Rendering;
  roundTrips: boolean;
}

/** A change the agent refused. The screens redraw from the agent after one, never from
 * the request that failed. */
export class Refusal extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

/** Everything the console asks the agent, over the routes it already serves. */
export interface AgentClient {
  snapshot(): Promise<Snapshot>;
  /** PUT /policy. Replaces the whole state. Resolves with what the agent applied. */
  setPolicy(policy: Policy): Promise<Health>;
  test(text: string): Promise<TestResult>;
  /** PUT /today: which families the menu bar's figure leaves out. Masking is not affected. */
  setUncounted(families: string[]): Promise<void>;
  /** POST /enrol: trades a single-use token for this agent's identity at a backend. */
  enrol(backend: string, token: string): Promise<void>;
  /** GET /report: the next report as it stands, in the shape it is sent. Drawn as it
   * comes, never read: the page holds no copy of the heartbeat's shape to drift. */
  nextReport(): Promise<unknown>;
  /** POST /report: has the reports that wait sent now rather than at the next attempt. */
  sendNow(): Promise<void>;
  /** GET /rtk: what rtk recorded on this workstation, day by day. Runs rtk. */
  rtk(): Promise<RtkReading>;
}
