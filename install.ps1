<#
.SYNOPSIS
Install the Neverseen agent as a background job on this workstation.

.DESCRIPTION
The same three things this deliberately does NOT do as install.sh, for the same
reasons:

  1. It does not put a base URL into your PowerShell profile. The project this one
     replaces did, and the day somebody stopped the proxy without running its
     uninstaller, every LLM tool on the machine broke with a connection error from a
     line they had not written. What goes into the profile instead is
     `neverseen env | Invoke-Expression`, which prints only comments while the agent
     is stopped — so the tools reach their provider directly, exactly as before.

  2. It does not touch your profile at all unless you ask, with -Shell.

  3. It does not enable supervision. That is the paid half, it needs a backend URL
     and an enrolment token, and it belongs in the config file rather than in an
     installer's arguments where it would land in your command history.

It writes no service definition of its own. The two logon tasks live in the agent,
behind `neverseen service` — one owner of the service definition, so that this script
and whatever else installs this agent cannot come to disagree about what is
registered.

.EXAMPLE
.\install.ps1
.EXAMPLE
.\install.ps1 -Shell
.EXAMPLE
.\install.ps1 -Uninstall
#>
[CmdletBinding()]
param(
    # Add the line to your PowerShell profile as well.
    [switch]$Shell,
    # Stop the agent, deregister it, and undo the profile line.
    [switch]$Uninstall,
    # Report whether it is running and what it is applying.
    [switch]$Status,
    # Where to install the binaries. The default matches install.sh.
    [string]$Prefix = (Join-Path $HOME '.local')
)

$ErrorActionPreference = 'Stop'

$BinDir     = Join-Path $Prefix 'bin'
$ConfigDir  = Join-Path $HOME '.neverseen'
$ConfigFile = Join-Path $ConfigDir '.env'
$Agent      = Join-Path $BinDir 'neverseen.exe'
$Tray       = Join-Path $BinDir 'neverseen-tray.exe'

# Matched verbatim when removing it, so it has to stay one line and stay recognisable.
$ShellLine = 'neverseen env | Invoke-Expression  # neverseen: prints nothing while the agent is stopped'

function Install-Binaries {
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

    $source = $PSScriptRoot
    foreach ($name in @('neverseen.exe', 'neverseen-tray.exe')) {
        $from = Join-Path $source $name
        if (-not (Test-Path $from)) {
            # The icon may legitimately be absent from a trimmed installation; the
            # agent may not. Registering nothing and reporting success is how somebody
            # ends up believing their traffic is masked.
            if ($name -eq 'neverseen.exe') { throw "no $name beside this script" }
            continue
        }
        Copy-Item -Path $from -Destination (Join-Path $BinDir $name) -Force
        Write-Host "Installed $name into $BinDir"
    }

    # install.sh has warned about this since it was written, and the Windows default is
    # worse: ~\.local\bin is on no fresh machine's PATH. Silent, -Shell then adds a
    # profile line calling a command that is not there, every new session opens on a
    # CommandNotFoundException, nothing is ever pointed at the agent — and the traffic
    # goes out unmasked while the install reported success.
    $onPath = ($env:PATH -split ';' | Where-Object { $_.TrimEnd('\') -ieq $BinDir.TrimEnd('\') })
    if (-not $onPath) {
        Write-Warning "$BinDir is not on your PATH, so ``neverseen`` will not be found yet."
        Write-Warning "Add it, or the profile line -Shell writes will fail in every new session."
    }
}

function Write-Config {
    New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null
    if (Test-Path $ConfigFile) {
        Write-Host "Kept your existing $ConfigFile"
        return
    }

    # Shipped with the question rather than an answer. Choosing a locale here would be
    # worse than leaving it: scanning one country's data with another country's
    # patterns masks its invoice numbers and misses its identifiers.
    @'
# Neverseen agent configuration. Every variable is documented in .env.example.

# Which country pattern sets to load: fr, gb, us, none, or a comma-separated list.
# Unset means none, which is an agent that answers and recognises almost nothing.
NEVERSEEN_PII_LOCALE=

NEVERSEEN_LISTEN=127.0.0.1:8787

# Supervision is optional and the agent is complete without it.
# NEVERSEEN_BACKEND_URL=
# NEVERSEEN_ENROLMENT_TOKEN=
'@ | Set-Content -Path $ConfigFile -Encoding UTF8

    Write-Host "Wrote $ConfigFile - set NEVERSEEN_PII_LOCALE in it, then re-run with -Status"
}

function Set-ShellLine {
    $profilePath = $PROFILE.CurrentUserAllHosts
    $profileDir = Split-Path -Parent $profilePath
    New-Item -ItemType Directory -Force -Path $profileDir | Out-Null

    if ((Test-Path $profilePath) -and (Select-String -Path $profilePath -Pattern 'neverseen env' -Quiet)) {
        Write-Host "Your $profilePath already evaluates neverseen env"
        return
    }

    Add-Content -Path $profilePath -Value "`n$ShellLine"
    Write-Host "Added the line to $profilePath"
    Write-Host "It is safe to leave there: with the agent stopped it prints only comments,"
    Write-Host "and your tools go straight to their provider - unmasked, but working."
}

function Remove-ShellLine {
    $profilePath = $PROFILE.CurrentUserAllHosts
    if (-not (Test-Path $profilePath)) { return }
    if (-not (Select-String -Path $profilePath -Pattern 'neverseen env' -Quiet)) { return }

    # Through a temporary file and a move, so an interrupted uninstall cannot leave a
    # truncated profile behind.
    $temp = "$profilePath.neverseen.tmp"
    Get-Content $profilePath | Where-Object { $_ -notmatch 'neverseen env' } | Set-Content $temp
    Move-Item -Path $temp -Destination $profilePath -Force
    Write-Host "Removed the line from $profilePath"
}

if ($Status) {
    # Observes and authors nothing, which is why this verb lives here rather than in
    # the agent beside install/uninstall/restart. It used to open with
    # `service install`, described as a no-op if already registered: it is not one.
    # That rewrites both task definitions, passes /F to schtasks so an existing one is
    # replaced rather than kept, and ends on /Run — so somebody who had deliberately
    # stopped the agent and ran this to check found it started again, and a
    # hand-edited definition silently overwritten by the act of looking at it.
    # Scoped back to Continue for this block alone. schtasks exits non-zero when a task
    # is not registered, which is the ordinary answer here rather than a failure — and
    # on PowerShell 7.4 and later a native command's exit code is honoured under
    # ErrorActionPreference Stop, so "not registered" would terminate the script
    # instead of being reported.
    $ErrorActionPreference = 'Continue'

    Write-Host "Service:"
    $tasks = schtasks /Query /TN Neverseen /FO LIST 2>&1
    if ($LASTEXITCODE -ne 0) { Write-Host "  not registered" } else { $tasks | Select-Object -First 4 }

    # Only where one was installed, which is the condition `neverseen service install`
    # uses to decide whether to register an icon at all — so the two cannot disagree
    # about whether one was expected.
    if (Test-Path $Tray) {
        $trayTask = schtasks /Query /TN NeverseenTray /FO LIST 2>&1
        if ($LASTEXITCODE -ne 0) { Write-Host "  the notification area icon is not registered" }
        else { $trayTask | Select-Object -First 4 }
    }

    # Through the agent's own command rather than a raw request: one place decides what
    # "working" means, and it tells an agent that is up from one that is up and
    # actually masking, which a 200 does not.
    Write-Host "Health:"
    if (Test-Path $Agent) { & $Agent status } else { Write-Host "  $Agent is not installed" }
    return
}

if ($Uninstall) {
    # Before the binaries go, because it is the agent that owns the definition and
    # removing it first would leave the two tasks registered with nothing to remove
    # them.
    try { & $Agent service uninstall --prefix $Prefix } catch { Write-Warning "could not remove the tasks: $_" }

    Remove-ShellLine
    Remove-Item -Path $Agent, $Tray -Force -ErrorAction SilentlyContinue
    Write-Host "Removed the binaries from $BinDir"

    # The config and the identity are left alone on purpose. The identity is what a
    # supervision backend knows this machine by, and deleting it silently would have
    # the next install enrol as a second agent and count twice against what you pay
    # for.
    Write-Host ""
    Write-Host "Left in place: $ConfigDir"
    Write-Host "  It holds your configuration and, if you were supervised, the identity"
    Write-Host "  the backend knows this machine by. Remove it yourself if you mean to."
    return
}

Install-Binaries
Write-Config
& $Agent service install --prefix $Prefix
if ($Shell) { Set-ShellLine }

Write-Host ""
Write-Host "Done. Open the test page to see what it would mask, with your own text:"
Write-Host "  http://127.0.0.1:8787/test"
Write-Host ""
Write-Host "Two things to expect on Windows:"
Write-Host "  - SmartScreen will warn on first run; these builds are not signed yet."
Write-Host "  - Windows 11 hides new notification area icons in the overflow flyout."
Write-Host "    If you cannot see it, open the flyout and drag it onto the taskbar."
