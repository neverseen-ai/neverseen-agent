package telemetry

// The closed vocabularies a heartbeat's tool and client counts are keyed on.
//
// They are here, in the contract, rather than in the agent, because both sides
// need them: the agent reduces what it observes to these words, and the backend
// renders and validates them. A key outside the list is Other, so a heartbeat
// carries no string a workstation typed — a tool name a client made up, a program
// nobody has heard of, a User-Agent somebody wrote by hand — which is what keeps
// TestHeartbeatCarriesNoContent true of a map whose keys the agent chooses at
// run time. Widening a list is a contract change: add the word, and the backend
// learns it in the same commit.

// Other is the key everything outside a vocabulary is counted under.
const Other = "other"

// KnownClients are the client families a User-Agent is reduced to.
//
// A family rather than a product with a version: the version is the part of a
// User-Agent a client is free to write anything into, and a fleet view asking
// "which tools" is answered by the family alone.
// TODO: no version, so a fleet running an out-of-date client is not visible here.
// The upgrade is a second map keyed by family and major version, once the major
// can be read out of every family's User-Agent reliably.
var KnownClients = []string{
	"claude-code",
	"anthropic-sdk",
	"openai-sdk",
	"cursor",
	"copilot",
	"continue",
	"aider",
	"codex",
	"gemini-cli",
	"opencode",
	"curl",
	"extension",
}

// KnownTools are the tool names a coding agent asks its client to run.
//
// Claude Code's set, because that is the shape the agent reads today; a name
// outside it is Other, and an MCP tool — `mcp__<server>__<tool>` — is "mcp",
// because the server half is a name the workstation's own configuration chose.
var KnownTools = []string{
	"Bash",
	"Read",
	"Write",
	"Edit",
	"MultiEdit",
	"Glob",
	"Grep",
	"WebFetch",
	"WebSearch",
	"Task",
	"Agent",
	"NotebookEdit",
	"TodoWrite",
	"mcp",
}

// KnownPrograms are the programs a shell command may be counted under.
//
// The first word of each command in a Bash tool call, after the shell's own
// operators have split it and the environment assignments, `sudo`, `time` and
// the like have been stepped over. Anything not listed is Other, and that is the
// rule that lets a heartbeat read a command line at all: the word "python3" comes
// from this list, not from the prompt.
var KnownPrograms = []string{
	// The shell and its coreutils.
	"sh", "bash", "zsh", "cat", "ls", "cd", "cp", "mv", "rm", "mkdir", "find",
	"grep", "sed", "awk", "cut", "sort", "uniq", "head", "tail", "wc", "tr",
	"xargs", "echo", "printf", "tee", "diff", "chmod", "chown", "tar", "zip",
	"unzip", "which", "env", "jq", "yq", "rg", "fd", "tree", "open",
	// Version control.
	"git", "gh", "glab", "svn", "hg",
	// Languages and their tooling.
	"python", "python3", "pip", "pip3", "uv", "poetry", "pytest",
	"node", "npm", "npx", "yarn", "pnpm", "bun", "deno", "tsc",
	"go", "gofmt", "golangci-lint",
	"cargo", "rustc",
	"java", "javac", "mvn", "gradle",
	"ruby", "gem", "bundle",
	"php", "composer",
	"dotnet",
	"make", "cmake", "gcc", "clang",
	// Network.
	"curl", "wget", "ssh", "scp", "rsync", "nc", "ping", "dig", "nslookup",
	"telnet", "openssl",
	// Containers and infrastructure.
	"docker", "docker-compose", "podman", "kubectl", "helm", "terraform",
	"ansible", "vagrant",
	// Cloud.
	"aws", "gcloud", "az", "flyctl", "vercel", "heroku",
	// Databases.
	"psql", "mysql", "mongosh", "mongo", "redis-cli", "sqlite3",
	// Secrets and identity.
	"vault", "op", "pass", "gpg", "security",
	// Privilege and packages.
	"sudo", "su", "doas", "brew", "apt", "apt-get", "yum", "dnf", "pacman",
	"snap", "systemctl", "launchctl",
}

// CommandClasses are the kinds of shell command counted in Tools.Classes.
//
// A command may fall into several. The class is decided from the program and its
// first few flags, never from the rest of the line.
var CommandClasses = []string{
	// Network reaches another machine: curl, wget, ssh, scp, rsync, nc.
	"network",
	// Privilege runs as another user: sudo, su, doas.
	"privilege",
	// Destructive removes recursively or by force: rm -r, rm -f, git push --force,
	// git reset --hard, git clean -f.
	"destructive",
	// Install fetches software: pip install, npm install, brew install, apt install,
	// go install, cargo install.
	"install",
	// Secrets reads a credential store: vault, op, pass, security, aws sts, gcloud
	// auth, kubectl get secret.
	"secrets",
	// PipeToShell feeds a download to an interpreter: curl … | sh.
	"pipe-to-shell",
}
