#!/bin/sh
# Install the Neverseen agent as a background service on this workstation.
#
# Three things this script deliberately does NOT do:
#
#   1. It does not export ANTHROPIC_BASE_URL into your shell profile. The project
#      this one replaces did, and the day somebody stopped the proxy without
#      running its uninstaller, every LLM tool on the machine broke with a
#      connection error from a line in a file they had not touched. What goes
#      into the profile instead is `eval "$(neverseen env)"`, which prints
#      nothing while the agent is stopped — so the tools reach their provider
#      directly, exactly as before it was installed.
#
#   2. It does not touch your profile at all unless you ask for it, with
#      --shell. Editing a login file is the kind of change that has to be
#      requested rather than assumed.
#
#   3. It does not enable supervision. That is the paid half, it needs a backend
#      URL and an enrolment token, and it belongs in the config file rather than
#      in an installer's arguments where it would land in your shell history.
#
# Where the binary comes from, in order: the checkout this script lives in if
# there is a Go toolchain, an unpacked release archive if it sits beside one, and
# otherwise the latest release, downloaded and verified. The third is what somebody
# piping this script from the web gets, and it used to be an error message.
#
# The usage is in usage() below rather than in this comment, because --help has to
# answer when this script is piped and there is no file to read it out of.

set -eu

# What every invocation can print, on disk or piped. The rationale above is the
# other half of --help and is only available when $0 is a file.
usage() {
    cat <<'EOF'
Usage:
  ./install.sh [--shell]     install, start, and optionally wire the shell
  ./install.sh --status      is it running, and what is it applying
  ./install.sh --restart     restart it (after editing the config)
  ./install.sh --logs        follow its log
  ./install.sh --uninstall   stop it, remove the services, undo the shell line
EOF
}

# Declared before the trap below is armed, never beside the code that fills
# them. cleanup reads both, so under set -u a failure between arming the trap
# and reaching those assignments died on an unbound variable inside the trap —
# which is a trap that fails changing the exit status of the script, the exact
# thing the `if`s below exist to avoid.
TEMP_DIR=""
# The name a binary is written under before it is renamed into place. Held in a
# variable so an interrupted install does not leave it beside the real one.
STAGED=""

# An interrupted download must not leave an unpacked release behind. An `if`
# rather than `[ … ] && …`: with set -e a false test as the last command of a
# function makes it fail, and a trap that fails changes the exit status of a
# script that had succeeded.
cleanup() {
    if [ -n "$TEMP_DIR" ]; then rm -rf "$TEMP_DIR"; fi
    if [ -n "$STAGED" ]; then rm -f "$STAGED"; fi
}
trap cleanup EXIT

BIN_NAME=neverseen
# The menu bar icon is its own binary, and only on macOS. Not because it cannot be
# built elsewhere — it is cgo on darwin alone, and it cross-compiles to Linux and
# Windows in pure Go — but because on Linux whether it is shown depends on the
# desktop: GNOME needs the AppIndicator extension for it. See internal/tray.
TRAY_NAME=neverseen-tray
PREFIX="${NEVERSEEN_PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
CONFIG_DIR="$HOME/.neverseen"
CONFIG_FILE="$CONFIG_DIR/.env"
LOG_FILE="$CONFIG_DIR/agent.log"

SERVICE_LABEL=ai.neverseen.agent
TRAY_LABEL=ai.neverseen.tray

# The line added to a profile. Matched verbatim on uninstall, so it has to stay
# one line and stay recognisable. The single quotes are the point: the command
# substitution must reach the profile unexpanded, to run at every login rather
# than once here — which is what shellcheck's SC2016 reads as a mistake.
# shellcheck disable=SC2016
SHELL_LINE='eval "$(neverseen env)"  # neverseen: prints nothing while the agent is stopped'

# Where a release is fetched from, and which one. Deliberately not in
# .env.example: these configure an installation and not the running agent, and
# TestDocumentedEnvironmentMatchesTheCode fails on a variable documented there
# that no code reads. NEVERSEEN_PREFIX above is the same case.
REPO="${NEVERSEEN_REPO:-neverseen-ai/neverseen-agent}"

# Where install_binary reads the binaries from: the directory this script lives
# in, or an unpacked archive fetch_release leaves in a temporary one. The script's
# own directory rather than the working one, because run from anywhere else the
# checkout went unseen and a release was downloaded over the code in front of it.
# Piped from the web, $0 is the shell's own name and not a file, and the working
# directory is all there is.
if [ -f "$0" ]; then
    SOURCE_DIR=$(cd "$(dirname "$0")" && pwd)
else
    SOURCE_DIR=.
fi

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { warn "install.sh: $*"; exit 1; }

platform() {
    case "$(uname -s)" in
        Darwin) echo darwin ;;
        Linux)  echo linux ;;
        *)      die "unsupported system $(uname -s); the agent runs on macOS and Linux" ;;
    esac
}

# Asked once, here, and not at each place that wants it. Called as a command
# substitution the refusal above exits the *subshell*: an unsupported system
# printed the message and carried on installing, because the test around it simply
# read an empty string. In an assignment, set -e takes the substitution's status
# and the script stops where it says it does.
PLATFORM=$(platform)

# ---------------------------------------------------------------- the binary

# latest_tag reads the tag off the redirect on /releases/latest.
#
# The redirect and not the REST API: anonymous API calls are capped at sixty an
# hour per address, and behind a company NAT that budget is spent by somebody
# else — the installer would then fail on a rate limit, which is not a sentence
# anybody can act on. Set NEVERSEEN_VERSION to pin a tag instead, which is also
# the way back to an older release.
#
# `sed -n …p` and not a bare substitution: a repository with no published release
# redirects to /releases rather than to /releases/tag/vX.Y.Z, the pattern then
# matches nothing, and sed prints the Location line unchanged. That non-empty
# string walked straight past the emptiness check its caller makes and went into a
# download URL — the confusing failure that check exists to replace.
latest_tag() {
    curl --proto '=https' --tlsv1.2 -fsSI "https://github.com/$REPO/releases/latest" \
        | grep -i '^location:' \
        | sed -n -E 's|.*/tag/([^[:space:]]+).*|\1|p' \
        | tr -d '\r'
}

# verify_checksum: the archive, the checksums file, the name to look up.
#
# There is no variable to switch this off, and that is deliberate. This binary
# forwards the caller's credentials to a provider; an escape hatch on the only
# integrity check the installation has is the line that ends up pasted into an
# internal wiki because somebody's proxy mangled a download once.
verify_checksum() {
    # sha256sum on GNU systems, shasum -a 256 on macOS. One of the two is always
    # there, and neither is on both.
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$1" | cut -d' ' -f1)
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$1" | cut -d' ' -f1)
    else
        die "neither sha256sum nor shasum is here, so the download cannot be verified"
    fi

    # awk on the second field rather than a grep pattern built from the name:
    # the name is full of dots, and as an ERE each one matches any character.
    # The leading `*` is how the coreutils format marks a binary read.
    expected=$(awk -v name="$3" '$2 == name || $2 == "*" name { print $1 }' "$2")
    [ -n "$expected" ] || die "$3 is not listed in checksums.txt; refusing to install it"
    # The likeliest cause by far is a proxy that served a cached archive from a
    # previous release, so the message names it: the alternative reading of a
    # checksum mismatch is alarming and almost never the right one.
    [ "$expected" = "$actual" ] || die "checksum mismatch for $3: expected $expected, got $actual (a caching proxy may have served a stale archive; retry, or set NEVERSEEN_VERSION to pin a tag)"
    say "Checksum verified."
}

# fetch_release downloads a release archive and unpacks it into a temporary
# directory, which becomes SOURCE_DIR.
fetch_release() {
    command -v curl >/dev/null 2>&1 || die "curl is needed to download a release"
    command -v tar >/dev/null 2>&1 || die "tar is needed to unpack a release"

    case "$(uname -m)" in
        x86_64|amd64)  arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) die "unsupported architecture $(uname -m)" ;;
    esac

    # `|| true` inside the substitution, and it is not decoration: under set -e an
    # assignment takes the status of the command substitution, so a 404 or an
    # unreachable GitHub would end the script silently, one line above the message
    # written to explain exactly that.
    tag="${NEVERSEEN_VERSION:-$(latest_tag || true)}"
    [ -n "$tag" ] || die "could not work out the latest version (no published release, or GitHub is unreachable); set NEVERSEEN_VERSION=vX.Y.Z"

    # goreleaser names an archive after the version without its leading v, while
    # the URL it sits at carries the tag with it.
    archive="${BIN_NAME}_${tag#v}_${PLATFORM}_${arch}.tar.gz"
    base="https://github.com/$REPO/releases/download/$tag"

    TEMP_DIR=$(mktemp -d)

    # --proto '=https' on every fetch: -L follows redirects, and without it a
    # redirect could hand the download to plain http, in front of the checksum
    # that is meant to be the only integrity check here.
    say "Downloading $archive ($tag)…"
    curl --proto '=https' --tlsv1.2 -fsSL "$base/$archive" -o "$TEMP_DIR/$archive" \
        || die "could not download $base/$archive"
    curl --proto '=https' --tlsv1.2 -fsSL "$base/checksums.txt" -o "$TEMP_DIR/checksums.txt" \
        || die "could not download checksums.txt; refusing to install an unverified binary"

    verify_checksum "$TEMP_DIR/$archive" "$TEMP_DIR/checksums.txt" "$archive"

    # Every entry, before anything is written. An archive naming an absolute path
    # or stepping out of the directory with .. has tar write wherever it likes,
    # and this script runs as the person whose home directory that is (CWE-22).
    if tar -tzf "$TEMP_DIR/$archive" | grep -qE '^/|(^|/)\.\.(/|$)'; then
        die "the archive names paths outside itself; refusing to unpack it"
    fi
    # And no links, which the check above cannot see. A symlink entry pointing at
    # a directory outside, followed by a plain file under that name, has every
    # entry looking relative while tar writes through the link — GNU tar does,
    # recent bsdtar refuses, and this script must not depend on which is here.
    # A release archive of two binaries has no business holding a link at all.
    if tar -tvzf "$TEMP_DIR/$archive" | grep -qE '^[lh]'; then
        die "the archive holds a link entry; refusing to unpack it"
    fi

    tar -xzf "$TEMP_DIR/$archive" -C "$TEMP_DIR"
    SOURCE_DIR="$TEMP_DIR"
}

# claim_the_name refuses to install behind another neverseen.
#
# Two binaries answering to one name is worse here than it is for an ordinary
# tool: this one is pointed at by ANTHROPIC_BASE_URL and forwards the caller's
# credential, so `neverseen status` reporting a healthy agent while a different
# build is the one on the PATH is a masking failure nobody would look for. Asked
# before anything is written, so the refusal costs nothing.
claim_the_name() {
    existing=$(command -v "$BIN_NAME" 2>/dev/null || true)
    [ -n "$existing" ] || return 0
    [ "$existing" != "$BIN_DIR/$BIN_NAME" ] || return 0
    die "a different $BIN_NAME already owns the name, at $existing.
  Remove it, or set NEVERSEEN_PREFIX to the prefix it lives under, so one binary owns \`$BIN_NAME\`."
}

install_binary() {
    mkdir -p "$BIN_DIR"

    # Built from source when this is a checkout, copied when it is an unpacked
    # release archive. One script for both, because the second case is the one
    # an operator actually runs and it must not need a Go toolchain.
    # Built from inside SOURCE_DIR, in a subshell: go needs the module's own
    # directory as its working one, and so does the git describe that stamps the
    # version — run from elsewhere it described whatever repository was there.
    # Written under a temporary name in the destination directory and renamed,
    # never straight over the file that is there: on an upgrade that file is the
    # running agent, and a half-written executable is a crash rather than an old
    # version. A rename within one directory is atomic, and the process already
    # running keeps the binary it started from until the service is restarted.
    STAGED="$BIN_DIR/$BIN_NAME.install.$$"

    if [ -f "$SOURCE_DIR/go.mod" ] && command -v go >/dev/null 2>&1; then
        say "Building from source…"
        (cd "$SOURCE_DIR" && go build -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
            -o "$STAGED" ./cmd/neverseen)
    else
        # No toolchain and no archive beside this script: fetch one. This is the
        # ordinary case for anybody who did not clone the repository, and it used
        # to be where the script gave up.
        if [ ! -f "$SOURCE_DIR/$BIN_NAME" ]; then
            fetch_release
        fi
        say "Installing the released binary…"
        cp "$SOURCE_DIR/$BIN_NAME" "$STAGED"
    fi

    chmod 0755 "$STAGED"
    mv -f "$STAGED" "$BIN_DIR/$BIN_NAME"
    STAGED=""
    say "Installed $BIN_DIR/$BIN_NAME"

    # The icon, on macOS only, and never a reason to fail. If it cannot be built or
    # is not in the archive, the agent is installed and masking anyway — the icon
    # is how somebody sees that, not part of it.
    if [ "$PLATFORM" = darwin ]; then
        STAGED="$BIN_DIR/$TRAY_NAME.install.$$"
        if [ -f "$SOURCE_DIR/go.mod" ] && command -v go >/dev/null 2>&1; then
            if (cd "$SOURCE_DIR" && go build -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
                -o "$STAGED" ./cmd/neverseen-tray 2>/dev/null); then
                chmod 0755 "$STAGED"
                mv -f "$STAGED" "$BIN_DIR/$TRAY_NAME"
                say "Installed $BIN_DIR/$TRAY_NAME"
            else
                # A failed build can still have written something under the
                # staged name, and it must not be left beside the real icon.
                rm -f "$STAGED"
                say "Could not build $TRAY_NAME (it needs a C toolchain); skipping the menu bar icon"
            fi
        elif [ -f "$SOURCE_DIR/$TRAY_NAME" ]; then
            cp "$SOURCE_DIR/$TRAY_NAME" "$STAGED"
            chmod 0755 "$STAGED"
            mv -f "$STAGED" "$BIN_DIR/$TRAY_NAME"
            say "Installed $BIN_DIR/$TRAY_NAME"
        fi
        STAGED=""
    fi

    case ":$PATH:" in
        *":$BIN_DIR:"*) ;;
        *) warn "Note: $BIN_DIR is not on your PATH, so \`neverseen\` will not be found yet." ;;
    esac
}

# ---------------------------------------------------------------- the config

write_config() {
    mkdir -p "$CONFIG_DIR"
    chmod 0700 "$CONFIG_DIR"

    if [ -f "$CONFIG_FILE" ]; then
        say "Keeping your existing $CONFIG_FILE"
        return
    fi

    # A locale has to be chosen, and choosing one here would be worse than
    # leaving it: scanning one country's data with another country's patterns
    # masks its invoice numbers and misses its identifiers. So the file ships
    # with the question rather than with an answer.
    cat > "$CONFIG_FILE" <<'EOF'
# Neverseen agent configuration. See .env.example for every variable.

# Which country's identifiers to look for: fr, gb, us, none, or a comma-separated
# mix. There is no sensible default — scanning one country's data with another
# country's patterns is worse than scanning none of it — so set this before you
# rely on the agent for anything.
NEVERSEEN_PII_LOCALE=

# Loopback only. Do not widen this: the agent forwards whatever credential its
# caller sent and scopes its session mapping by a header the caller controls, so
# it is built for one person on one machine.
NEVERSEEN_LISTEN=127.0.0.1:9787

# Supervision, if you have a backend. Both blank means the agent runs standalone,
# which is a complete product rather than a disabled one.
# NEVERSEEN_BACKEND_URL=
# NEVERSEEN_ENROLMENT_TOKEN=
EOF
    chmod 0600 "$CONFIG_FILE"
    say "Wrote $CONFIG_FILE — set NEVERSEEN_PII_LOCALE in it, then --restart"
}

# ---------------------------------------------------------------- the service
#
# Not written here. The launchd plist, the systemd unit and the Windows scheduled
# task all live in the agent, behind `neverseen service` — one owner of the service
# definition. As heredocs in this script they could only be copied by anything else
# that wanted to install this agent, and the two that drifted would be the one that
# installed it and the one that restarted it.
#
# What stays here is what this script genuinely owns: placing the binaries, writing
# the config, wiring the shell, and observing (--status, --logs) which authors
# nothing.

service_cmd() {
    "$BIN_DIR/$BIN_NAME" service "$1" --prefix "$PREFIX"
}

# ---------------------------------------------------------------- the shell

wire_shell() {
    profile=""
    for candidate in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
        [ -f "$candidate" ] && { profile="$candidate"; break; }
    done
    [ -n "$profile" ] || { warn "No shell profile found; add this line yourself:"; say "  $SHELL_LINE"; return; }

    if grep -qxF "$SHELL_LINE" "$profile" 2>/dev/null; then
        say "Your $profile already evaluates \`neverseen env\`"
        return
    fi

    printf '\n%s\n' "$SHELL_LINE" >> "$profile"
    say "Added the export line to $profile"
    say "It is safe to leave there: with the agent stopped it prints nothing, and"
    say "your tools go straight to their provider — unmasked, but working."
}

unwire_shell() {
    for profile in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
        [ -f "$profile" ] || continue
        grep -qxF "$SHELL_LINE" "$profile" 2>/dev/null || continue

        # The whole line, anchored (-x), and never the substring `neverseen env`:
        # that substring matched a line somebody had written themselves — an alias,
        # or this one commented out — and --uninstall deleted it from their login
        # file without saying so. A line from an older release whose comment has
        # since changed is left behind instead, which costs nothing: with the agent
        # stopped it prints nothing, which is the whole point of the line.
        #
        # Written to a temporary file and moved, so an interrupted uninstall
        # cannot leave a truncated login file behind.
        # cp -p first so the copy carries the profile's own mode, which is the
        # portable spelling of it.
        #
        # The exit status is read rather than discarded, and the two non-zero ones
        # mean opposite things. 1 is grep selecting nothing — a profile holding only
        # that line — and the empty result is correct; under set -e that status alone
        # skipped the move and left the temporary file beside an untouched profile.
        # 2 and above is grep failing: an unreadable file, an I/O error. The
        # redirection has already truncated the temporary file by then, so moving it
        # on a failure installs an empty file over the profile — `|| :` swallowed both
        # statuses and did exactly that.
        tmp="$profile.neverseen.$$"
        cp -p "$profile" "$tmp"
        rc=0
        grep -vxF "$SHELL_LINE" "$profile" > "$tmp" || rc=$?
        if [ "$rc" -gt 1 ]; then
            rm -f "$tmp"
            warn "Could not read $profile (grep exited $rc); left it untouched."
            warn "Remove the 'neverseen env' line by hand."
            continue
        fi
        mv "$tmp" "$profile"
        say "Removed the export line from $profile"
    done
}

# ---------------------------------------------------------------- commands

do_install() {
    claim_the_name
    install_binary
    write_config

    service_cmd install

    [ "${WIRE_SHELL:-0}" = 1 ] && wire_shell

    # The address and the test page are in the status below, which reads them
    # from the agent itself rather than from this script's reading of the config.
    say ""
    say "Done."

    verify_running
}

# verify_running asks the agent what it is doing, rather than assuming the
# service registration means it is doing anything.
#
# Through `neverseen status` for the reason do_status gives: one place decides
# what "working" means, and it tells an agent that is up from one that is up and
# actually masking, which a 200 does not.
#
# It never fails the installation. The config written above ships with no locale
# chosen — on purpose, because choosing one here would be worse — so a fresh
# install is an agent that is running and recognising almost nothing, and that is
# a non-zero exit by design. An installer that treated it as a failure would be
# reporting the one thing that is working as broken.
#
# Which is also why the wait below spends its whole budget on a first install:
# the exit code is the only signal `status` gives, and it cannot say "not there
# yet" apart from "there, with no locale". A few seconds is worth not parsing the
# status prose, which is written for a person and free to change.
verify_running() {
    tries=0
    while [ "$tries" -lt 5 ]; do
        if "$BIN_DIR/$BIN_NAME" status >/dev/null 2>&1; then
            break
        fi
        tries=$((tries + 1))
        sleep 1
    done

    say ""
    "$BIN_DIR/$BIN_NAME" status || true
}

do_status() {
    say "Service:"
    case "$PLATFORM" in
        darwin)
            launchctl list | grep -F "$SERVICE_LABEL" || say "  not loaded"
            # An `if`, not `[ … ] && …`: with set -e a false test at the end of this
            # branch is a failing compound, and the script would exit on a machine
            # that simply has no icon installed.
            # The same condition `neverseen service install` uses to decide whether
            # to register an icon at all, so the two cannot disagree about whether
            # one was expected.
            if [ -x "$BIN_DIR/$TRAY_NAME" ]; then
                launchctl list | grep -F "$TRAY_LABEL" || say "  the menu bar icon is not loaded"
            fi
            ;;
        linux)  systemctl --user is-active neverseen.service || true ;;
    esac

    # Through the agent's own command rather than curl and a raw body. One place
    # decides what "working" means, and it distinguishes an agent that is up from
    # one that is up and actually masking, which a 200 does not.
    #
    # Called bare: the command reads ~/.neverseen/.env itself, so the address is
    # not scraped out of the file here. That scrape was a second reader of the
    # config with its own idea of the syntax — it broke on a quoted value the
    # agent accepted, and reported the wrong port as not answering.
    say "Health:"
    "$BIN_DIR/$BIN_NAME" status || true
}

do_restart() {
    service_cmd restart
}

do_logs() {
    case "$PLATFORM" in
        darwin) [ -f "$LOG_FILE" ] || die "no log at $LOG_FILE yet"; tail -f "$LOG_FILE" ;;
        linux)  journalctl --user -u neverseen.service -f ;;
    esac
}

do_uninstall() {
    # Before the binaries go, because it is the agent that owns the definition and
    # removing it first would leave the registration behind with nothing to remove it.
    service_cmd uninstall || warn "could not remove the service; continuing"

    unwire_shell
    rm -f "$BIN_DIR/$BIN_NAME" "$BIN_DIR/$TRAY_NAME"
    say "Removed $BIN_DIR/$BIN_NAME and, if it was there, $BIN_DIR/$TRAY_NAME"

    # The config and the identity are left alone on purpose. The config is the
    # operator's own work, and the identity is what a supervision backend knows
    # this machine by — deleting it silently would have the next install enrol as
    # a second agent and count twice against whatever they are paying for.
    say ""
    say "Left in place: $CONFIG_DIR"
    say "  It holds your configuration and, if you were supervised, the identity"
    say "  the backend knows this machine by. Remove it yourself if you mean to."
}

WIRE_SHELL=0
# One verb per invocation, and the rest is refused rather than dropped:
# `--shell --status` installed and wired the shell, and said nothing about the
# status it had been asked for.
[ "$#" -le 1 ] || die "one option at a time (got $#; try --help)"
case "${1:---install}" in
    --install)   do_install ;;
    --shell)     WIRE_SHELL=1; do_install ;;
    --status)    do_status ;;
    --restart)   do_restart ;;
    --logs)      do_logs ;;
    --uninstall) do_uninstall ;;
    # Piped from the web, $0 is the shell's own name and the rationale at the head
    # of this script is not on disk to be read — sed then failed on a file called
    # `sh`, in the one invocation where printing something is the whole request.
    -h|--help)
        if [ -f "$0" ]; then sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'; fi
        usage
        ;;
    *)           die "unknown option $1 (try --help)" ;;
esac
