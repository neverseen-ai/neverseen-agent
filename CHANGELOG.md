# Changelog

What changes for somebody running the agent, release by release, newest first.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); each entry ends with the commits it was written from.

## [Unreleased]

### Security

- On Linux, another account on the same machine can no longer send requests through the agent and read a restored answer, and no surface hands the control key to an agent another account started (563650d, 4bb4da5)
- The settings page carries the control key only when opened through a one-time link, valid for a minute, from the menu bar or `neverseen settings` (563650d)
- The browser extension sends nothing to a listener that has not first proved it holds the control key (563650d, 4bb4da5)
- A web page that rebinds its own domain to this machine can no longer use the agent and read a restored answer: a request whose `Host` is not the agent's is refused with a 421 (419f691)
- An AWS secret key is masked where no `=` or `:` follows its name — `aws configure set`, a command-line flag, the console's `credentials.csv`, a sentence naming it beside its access key id (d21e07f)
- A telephone number, a date of birth or a postcode assigned to a field named for it is masked inside source code too, where a customer record in a test fixture used to go out in clear (493e678)
- Danish licence plates are masked in two more notations, and a mistyped plate notation can no longer leave a country's plates unmasked in silence (efbaf42)
- A string the model wrote in a tool call's arguments can no longer reach the supervision report as a model name, nor a nested `usage` object be added to the token counts (397afc2)

### Added

- The agent log says when a request carries a picture or other encoded content the agent cannot read, and when an answer uses a placeholder the agent never gave (52b65c5)
- `neverseen settings` prints the link that opens the settings page (563650d)
- The settings page enrols a running agent with a supervision backend, with no edit of `~/.neverseen/.env` and no restart, and says why when the token is refused (4bb4da5)
- The settings page shows the tokens each model was sent and answered, for the day and by hour (4bb4da5)

### Changed

- The settings page is a new console, with a screen to try a text in both substitution modes and the day's replacements drawn by hour and by category (563650d, 2f5f079)
- The day's count is no longer on `/healthz`: it is served behind the control key, counts a value once per session rather than on every turn, and the settings page chooses which families the menu bar counts (563650d)
- A conversation keeps its placeholders for three hours of silence rather than thirty minutes (da231dc)

### Fixed

- A placeholder the model remembers is restored after a long pause, where it used to come back unexpanded once the session had expired (da231dc)
- A restart no longer gives a placeholder a conversation remembers to another value, two requests in flight no longer give one value two names, and a conversation resumed after a restart gets its placeholders back; uninstalling deletes the record that makes this possible (6fd0595, d4b50b2, 8819669, 4bb4da5)
- A value is never given a placeholder the conversation already carries as text, which the answer would have restored as that value (63d9156, 4bb4da5)
- A request body with nothing to mask is forwarded as the bytes that arrived, rather than re-encoded (465c77e)
- A request body is refused with a 413 above 32 MiB, measured after decompression, where a small compressed body could exhaust memory (3490ebb)
- A UK National Insurance number no longer takes the word that follows it into the mask (9015bda)
- The browser extension restores values in the visible thinking, not only in the answer (0aa24c5)
- `neverseen service install` works without `~/.neverseen/.env`, whose absence used to put the agent in a restart loop (bde3758)
- `install.sh --uninstall` removes the service registration even when the binary is already gone (8f57832)
- `neverseen service restart` restarts the menu bar icon on Windows too, as on macOS (876fcd0)
- The menu bar redraws a change made on the settings page after one of its own clicks, where it could go on saying "Masking" with categories switched off (4bb4da5)
- The menu bar's links open for an agent bound to every address, such as `:9787` (4bb4da5)
- A change the agent refuses redraws the settings page from what the agent applied (9c6bba0)
- `neverseen mask` wraps its country lists instead of printing one line of several hundred columns (f53f678)
- An agent whose disk refused its supervision identity keeps reporting and retries the save, rather than being unable to enrol again (5a1a953)
- Each supervision report leaves as its window closes, rather than drifting a whole interval late after a few days (c873a17)

## [0.2.0] - 2026-09-28

### Added

- Licence plates for every European country — forty country sets, among them `es`, `be`, `it`, `nl` and `pt` — and the British registration mark (556dc41, db9d937, 3e235c8, 70f8b59, cee3688, 9231a06, b3bafb8)
- `eu` as a locale selects every European country set, and the installers write it in the configuration they create (d05e7a6, 7038683)
- The supervision report says what rtk saved on the workstation (886224f, 9ebee5c, d07adf2)

### Fixed

- A French licence plate is read in the notations it is typed in, and no longer in ones it never takes (7482d58)

## [0.1.2] - 2026-09-22

### Added

- A fourth menu bar icon, for no agent answering at all (3a1ae45)
- A coloured icon set on Linux, visible on a light and on a dark panel (dc3e83d)

## [0.1.1] - 2026-09-22

### Added

- The menu bar icon on Linux, which says so when the desktop has nothing to draw it with (62b4a94)

## [0.1.0] - 2026-09-22

### Added

- A local proxy between an AI tool and its provider: personal data and credentials are replaced on the way out and restored on the way back, in buffered and streamed answers and in tool calls, with the caller's API key forwarded untouched (7db3e9e, 211f8c2, 954db70, 3c5b548, 835f2a4, 6d0f50d)
- A catalogue of personal data — addresses, places written as coordinates or map links, dates of birth, telephone numbers, IBANs, cards, IP addresses and national identifiers by country — checked by checksum and length where the identifier has them (6196c36, bba6489, 4c6963e, 9c793d8, e03ae4c, 591fee2, 771920e, 5c3fa85, 1c15860, 68649a3, 0faefa9, 34f3de8, 5f3794c)
- A catalogue of credentials — vendor API keys, private keys, connection strings, session cookies and secrets recognised by the name they are assigned to, with a strength level for the last (205403a, 58dd718, 5afd4ef, 4edac8d, 8016862, 77873fc, 581ebb2, 8b721ad, f152922, 4a640ef)
- A value inside source code is judged as source code, and by where it sits as well as what it is (62f9b79, 04f96fc)
- A test page that renders one text in both substitution modes and can simulate a configuration without applying it (9da5baf, e05d97a, b6a406e)
- What the agent masks can be changed while it runs, by family and by category, from `neverseen mask` (b3573bd, 402b616, 38816cb, 3db92d0, 8dd15e0)
- A settings page for the configuration (70b7fb0)
- A menu bar icon that says whether the agent is masking and hands over the command that runs a tool through it (e6e8031, 61637df, b525eda)
- `-a` prints every value replaced and restored, and `-v` writes every exchange to `traces/` with what it cost (a152c61, 6f1d83d, 002abd5)
- `-l` listens beyond loopback, with a warning (76503ad)
- A browser extension that masks what is typed into a web chat through the local agent, and refuses a send it cannot mask (d4b8e53, 32c21ff, c0a094c)
- Optional supervision: counts and category names, never content, reported every five minutes and queued on disk through an outage (ff156f5, 036220a, f946ce0, e7c231d, 65628dc, bd1ff10, a985906, 02a27a7, 4d7fec4, f887138)
- `neverseen replay` rebuilds a supervision report from a directory of traces (ab8d9ad)
- An installer and a service on macOS, Linux and Windows, which fetches and verifies a release when there is no toolchain and adds one line to a shell profile only when asked (4fe851b, 442aed6, 8321f34, b651364, 6cb7d88, cd9eade)
