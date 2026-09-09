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
# Usage:
#   ./install.sh [--shell]     install, start, and optionally wire the shell
#   ./install.sh --status      is it running, and what is it applying
#   ./install.sh --restart     restart it (after editing the config)
#   ./install.sh --logs        follow its log
#   ./install.sh --uninstall   stop it, remove the services, undo the shell line

set -eu

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
# one line and stay recognisable.
SHELL_LINE='eval "$(neverseen env)"  # neverseen: prints nothing while the agent is stopped'

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

# ---------------------------------------------------------------- the binary

install_binary() {
    mkdir -p "$BIN_DIR"

    # Built from source when this is a checkout, copied when it is an unpacked
    # release archive. One script for both, because the second case is the one
    # an operator actually runs and it must not need a Go toolchain.
    if [ -f ./go.mod ] && command -v go >/dev/null 2>&1; then
        say "Building from source…"
        go build -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
            -o "$BIN_DIR/$BIN_NAME" ./cmd/neverseen
    elif [ -f "./$BIN_NAME" ]; then
        say "Installing the bundled binary…"
        cp "./$BIN_NAME" "$BIN_DIR/$BIN_NAME"
    else
        die "no ./$BIN_NAME beside this script and no Go toolchain to build one"
    fi

    chmod 0755 "$BIN_DIR/$BIN_NAME"
    say "Installed $BIN_DIR/$BIN_NAME"

    # The icon, on macOS only, and never a reason to fail. If it cannot be built or
    # is not in the archive, the agent is installed and masking anyway — the icon
    # is how somebody sees that, not part of it.
    if [ "$(platform)" = darwin ]; then
        if [ -f ./go.mod ] && command -v go >/dev/null 2>&1; then
            if go build -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
                -o "$BIN_DIR/$TRAY_NAME" ./cmd/neverseen-tray 2>/dev/null; then
                chmod 0755 "$BIN_DIR/$TRAY_NAME"
                say "Installed $BIN_DIR/$TRAY_NAME"
            else
                say "Could not build $TRAY_NAME (it needs a C toolchain); skipping the menu bar icon"
            fi
        elif [ -f "./$TRAY_NAME" ]; then
            cp "./$TRAY_NAME" "$BIN_DIR/$TRAY_NAME"
            chmod 0755 "$BIN_DIR/$TRAY_NAME"
            say "Installed $BIN_DIR/$TRAY_NAME"
        fi
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
NEVERSEEN_LISTEN=127.0.0.1:8787

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

    if grep -qF 'neverseen env' "$profile" 2>/dev/null; then
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
        grep -qF 'neverseen env' "$profile" 2>/dev/null || continue

        # Written to a temporary file and moved, so an interrupted uninstall
        # cannot leave a truncated login file behind.
        tmp="$profile.neverseen.$$"
        grep -vF 'neverseen env' "$profile" > "$tmp" && mv "$tmp" "$profile"
        say "Removed the export line from $profile"
    done
}

# ---------------------------------------------------------------- commands

do_install() {
    install_binary
    write_config

    service_cmd install

    [ "${WIRE_SHELL:-0}" = 1 ] && wire_shell

    say ""
    say "Done. The agent is running on $(grep -E '^NEVERSEEN_LISTEN' "$CONFIG_FILE" | cut -d= -f2)."
    say "Open its test page to see what it would mask, with your own text:"
    say "  http://$(grep -E '^NEVERSEEN_LISTEN' "$CONFIG_FILE" | cut -d= -f2)/test"
}

do_status() {
    addr=$(grep -E '^NEVERSEEN_LISTEN' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2)
    addr=${addr:-127.0.0.1:8787}

    say "Service:"
    case "$(platform)" in
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
    # The address is passed in because the command reads it from the environment
    # and this script is not the service: the variable lives in the config file,
    # which nothing has sourced here.
    say "Health:"
    NEVERSEEN_LISTEN="$addr" "$BIN_DIR/$BIN_NAME" status || true
}

do_restart() {
    service_cmd restart
}

do_logs() {
    case "$(platform)" in
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
case "${1:---install}" in
    --install)   do_install ;;
    --shell)     WIRE_SHELL=1; do_install ;;
    --status)    do_status ;;
    --restart)   do_restart ;;
    --logs)      do_logs ;;
    --uninstall) do_uninstall ;;
    -h|--help)   sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//' ;;
    *)           die "unknown option $1 (try --help)" ;;
esac
