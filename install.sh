#!/bin/sh
# Install the Cloakfleet agent as a background service on this workstation.
#
# Three things this script deliberately does NOT do:
#
#   1. It does not export ANTHROPIC_BASE_URL into your shell profile. The project
#      this one replaces did, and the day somebody stopped the proxy without
#      running its uninstaller, every LLM tool on the machine broke with a
#      connection error from a line in a file they had not touched. What goes
#      into the profile instead is `eval "$(cloakfleet env)"`, which prints
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
#   ./install.sh --uninstall   stop it, remove the service, undo the shell line

set -eu

BIN_NAME=cloakfleet
PREFIX="${CLOAKFLEET_PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
CONFIG_DIR="$HOME/.cloakfleet"
CONFIG_FILE="$CONFIG_DIR/.env"
LOG_FILE="$CONFIG_DIR/agent.log"

SERVICE_LABEL=ai.cloakfleet.agent
LAUNCH_AGENT="$HOME/Library/LaunchAgents/$SERVICE_LABEL.plist"
SYSTEMD_UNIT="$HOME/.config/systemd/user/cloakfleet.service"

# The line added to a profile. Matched verbatim on uninstall, so it has to stay
# one line and stay recognisable.
SHELL_LINE='eval "$(cloakfleet env)"  # cloakfleet: prints nothing while the agent is stopped'

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
            -o "$BIN_DIR/$BIN_NAME" ./cmd/cloakfleet
    elif [ -f "./$BIN_NAME" ]; then
        say "Installing the bundled binary…"
        cp "./$BIN_NAME" "$BIN_DIR/$BIN_NAME"
    else
        die "no ./$BIN_NAME beside this script and no Go toolchain to build one"
    fi

    chmod 0755 "$BIN_DIR/$BIN_NAME"
    say "Installed $BIN_DIR/$BIN_NAME"

    case ":$PATH:" in
        *":$BIN_DIR:"*) ;;
        *) warn "Note: $BIN_DIR is not on your PATH, so \`cloakfleet\` will not be found yet." ;;
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
# Cloakfleet agent configuration. See .env.example for every variable.

# Which country's identifiers to look for: fr, gb, us, none, or a comma-separated
# mix. There is no sensible default — scanning one country's data with another
# country's patterns is worse than scanning none of it — so set this before you
# rely on the agent for anything.
CLOAKFLEET_PII_LOCALE=

# Loopback only. Do not widen this: the agent forwards whatever credential its
# caller sent and scopes its session mapping by a header the caller controls, so
# it is built for one person on one machine.
CLOAKFLEET_LISTEN=127.0.0.1:8787

# Supervision, if you have a backend. Both blank means the agent runs standalone,
# which is a complete product rather than a disabled one.
# CLOAKFLEET_BACKEND_URL=
# CLOAKFLEET_ENROLMENT_TOKEN=
EOF
    chmod 0600 "$CONFIG_FILE"
    say "Wrote $CONFIG_FILE — set CLOAKFLEET_PII_LOCALE in it, then --restart"
}

# ---------------------------------------------------------------- the service

install_service_darwin() {
    mkdir -p "$(dirname "$LAUNCH_AGENT")"
    cat > "$LAUNCH_AGENT" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$SERVICE_LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string><string>-c</string>
    <string>set -a; . "$CONFIG_FILE"; set +a; exec "$BIN_DIR/$BIN_NAME" proxy</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$LOG_FILE</string>
  <key>StandardErrorPath</key><string>$LOG_FILE</string>
</dict>
</plist>
EOF
    launchctl unload "$LAUNCH_AGENT" 2>/dev/null || true
    launchctl load -w "$LAUNCH_AGENT"
    say "Loaded the launchd agent $SERVICE_LABEL"
}

install_service_linux() {
    command -v systemctl >/dev/null 2>&1 || die "systemctl not found; run \`cloakfleet proxy\` yourself"

    mkdir -p "$(dirname "$SYSTEMD_UNIT")"
    cat > "$SYSTEMD_UNIT" <<EOF
[Unit]
Description=Cloakfleet agent
After=network-online.target

[Service]
EnvironmentFile=$CONFIG_FILE
ExecStart=$BIN_DIR/$BIN_NAME proxy
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF
    systemctl --user daemon-reload
    systemctl --user enable --now cloakfleet.service
    say "Enabled the systemd user service cloakfleet.service"
}

# ---------------------------------------------------------------- the shell

wire_shell() {
    profile=""
    for candidate in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
        [ -f "$candidate" ] && { profile="$candidate"; break; }
    done
    [ -n "$profile" ] || { warn "No shell profile found; add this line yourself:"; say "  $SHELL_LINE"; return; }

    if grep -qF 'cloakfleet env' "$profile" 2>/dev/null; then
        say "Your $profile already evaluates \`cloakfleet env\`"
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
        grep -qF 'cloakfleet env' "$profile" 2>/dev/null || continue

        # Written to a temporary file and moved, so an interrupted uninstall
        # cannot leave a truncated login file behind.
        tmp="$profile.cloakfleet.$$"
        grep -vF 'cloakfleet env' "$profile" > "$tmp" && mv "$tmp" "$profile"
        say "Removed the export line from $profile"
    done
}

# ---------------------------------------------------------------- commands

do_install() {
    install_binary
    write_config

    case "$(platform)" in
        darwin) install_service_darwin ;;
        linux)  install_service_linux ;;
    esac

    [ "${WIRE_SHELL:-0}" = 1 ] && wire_shell

    say ""
    say "Done. The agent is running on $(grep -E '^CLOAKFLEET_LISTEN' "$CONFIG_FILE" | cut -d= -f2)."
    say "Open its test page to see what it would mask, with your own text:"
    say "  http://$(grep -E '^CLOAKFLEET_LISTEN' "$CONFIG_FILE" | cut -d= -f2)/test"
}

do_status() {
    addr=$(grep -E '^CLOAKFLEET_LISTEN' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2)
    addr=${addr:-127.0.0.1:8787}

    say "Service:"
    case "$(platform)" in
        darwin) launchctl list | grep -F "$SERVICE_LABEL" || say "  not loaded" ;;
        linux)  systemctl --user is-active cloakfleet.service || true ;;
    esac

    say "Health:"
    if curl -fsS --max-time 2 "http://$addr/healthz" 2>/dev/null; then
        :
    else
        say "  not answering on $addr"
    fi
}

do_restart() {
    case "$(platform)" in
        darwin)
            launchctl unload "$LAUNCH_AGENT" 2>/dev/null || true
            launchctl load -w "$LAUNCH_AGENT"
            ;;
        linux) systemctl --user restart cloakfleet.service ;;
    esac
    say "Restarted."
}

do_logs() {
    case "$(platform)" in
        darwin) [ -f "$LOG_FILE" ] || die "no log at $LOG_FILE yet"; tail -f "$LOG_FILE" ;;
        linux)  journalctl --user -u cloakfleet.service -f ;;
    esac
}

do_uninstall() {
    case "$(platform)" in
        darwin)
            launchctl unload "$LAUNCH_AGENT" 2>/dev/null || true
            rm -f "$LAUNCH_AGENT"
            ;;
        linux)
            systemctl --user disable --now cloakfleet.service 2>/dev/null || true
            rm -f "$SYSTEMD_UNIT"
            systemctl --user daemon-reload 2>/dev/null || true
            ;;
    esac
    say "Stopped and removed the service."

    unwire_shell
    rm -f "$BIN_DIR/$BIN_NAME"
    say "Removed $BIN_DIR/$BIN_NAME"

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
