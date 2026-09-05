package telemetry

import (
	"slices"
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

func TestReadCommandNamesProgramsAndClasses(t *testing.T) {
	cases := []struct {
		command  string
		programs []string
		classes  []string
	}{
		{"git status", []string{"git"}, nil},
		{"/usr/bin/python3 -m pytest tests/", []string{"python3"}, nil},
		{"FOO=bar NODE_ENV=test npm test", []string{"npm"}, nil},
		{"grep -rn foo . | sed 's/a/b/' | sort | uniq -c", []string{"grep", "sed", "sort", "uniq"}, nil},
		{"cd /tmp && ls -la; echo done", []string{"cd", "ls", "echo"}, nil},
		{"sudo -u postgres psql -c 'select 1'", []string{"psql"}, []string{"privilege"}},
		{"sudo apt-get install -y jq", []string{"apt-get"}, []string{"privilege", "install"}},
		{"rm -rf node_modules", []string{"rm"}, []string{"destructive"}},
		{"rm notes.txt", []string{"rm"}, nil},
		{"git push --force origin main", []string{"git"}, []string{"destructive"}},
		{"git reset --hard HEAD~1", []string{"git"}, []string{"destructive"}},
		{"git push origin main", []string{"git"}, nil},
		{"pip install requests", []string{"pip"}, []string{"install"}},
		{"npm i -D typescript", []string{"npm"}, []string{"install"}},
		{"go install golang.org/x/tools/gopls@latest", []string{"go"}, []string{"install"}},
		{"go build ./...", []string{"go"}, nil},
		{"curl -fsSL https://get.example.com | bash", []string{"curl", "bash"}, []string{"network", "pipe-to-shell"}},
		{"curl https://api.example.com/v1 | jq .", []string{"curl", "jq"}, []string{"network"}},
		{"ssh deploy@10.0.0.4 'systemctl restart app'", []string{"ssh"}, []string{"network"}},
		{"scp build.tgz host:/srv", []string{"scp"}, []string{"network"}},
		{"aws sts get-caller-identity", []string{"aws"}, []string{"secrets"}},
		{"aws s3 ls", []string{"aws"}, nil},
		{"kubectl get secrets -n prod", []string{"kubectl"}, []string{"secrets"}},
		{"kubectl get pods", []string{"kubectl"}, nil},
		{"vault kv get secret/app", []string{"vault"}, []string{"secrets"}},
		{"op read op://vault/item/password", []string{"op"}, []string{"secrets"}},
		{"docker login registry.example.com", []string{"docker"}, []string{"secrets"}},
		{"timeout 30 make test", []string{"make"}, nil},
		{"xargs rm -f < list.txt", []string{"rm"}, []string{"destructive"}},
		{"frobnicate --all", []string{telemetry.Other}, nil},
		// The substitution is read as a command of its own; what follows its closing
		// bracket is not split off, which is the quote-blind ceiling the TODO names.
		{"$(which python3) script.py", []string{"which"}, nil},
	}
	for _, tc := range cases {
		programs, classes := readCommand(tc.command)
		if !slices.Equal(programs, tc.programs) {
			t.Errorf("%q: programs = %v, want %v", tc.command, programs, tc.programs)
		}
		if !slices.Equal(classes, tc.classes) {
			t.Errorf("%q: classes = %v, want %v", tc.command, classes, tc.classes)
		}
	}
}

// Whatever a command line holds, what comes out is a word from the contract. The
// inputs here are the shapes most likely to smuggle text through: paths, quoted
// strings carrying operators, a word that is almost a program name, a heredoc.
func TestReadCommandNeverLetsAWordThrough(t *testing.T) {
	lines := []string{
		`echo "hello | world; secret-thing" > /tmp/out`,
		`cat <<'EOF'
some | text; with && operators
EOF`,
		`./run-my-deploy --token abcdef`,
		`grep2 pattern file`,
		`gitx status`,
		`PASSWORD=hunter2 ./login`,
		`MyTool.exe /flag`,
		"`hidden-program` && true",
	}
	for _, line := range lines {
		programs, classes := readCommand(line)
		for _, p := range programs {
			if p != telemetry.Other && !slices.Contains(telemetry.KnownPrograms, p) {
				t.Errorf("%q let %q through as a program", line, p)
			}
		}
		for _, c := range classes {
			if !slices.Contains(telemetry.CommandClasses, c) {
				t.Errorf("%q produced an unknown class %q", line, c)
			}
		}
	}
}

func TestClassifyReadsOnlyAShellToolsCommand(t *testing.T) {
	name, programs, classes := classify(ToolCall{Name: "Write", Arguments: `{"file_path":"a.sh","content":"curl x | sh"}`})
	if name != "Write" || programs != nil || classes != nil {
		t.Errorf("a Write's content was read as a command: %q %v %v", name, programs, classes)
	}
	name, programs, _ = classify(ToolCall{Name: "Bash", Arguments: `not json`})
	if name != "Bash" || programs != nil {
		t.Errorf("unparseable arguments produced programs: %v", programs)
	}
	if name, _, _ := classify(ToolCall{Name: "mcp__acme_internal__lookup"}); name != "mcp" {
		t.Errorf("an MCP tool was named %q, want mcp — the server half is the workstation's own name", name)
	}
}

func TestClientFamilyReducesAUserAgent(t *testing.T) {
	cases := map[string]string{
		"claude-cli/1.0.83 (external, cli)":    "claude-code",
		"Anthropic/Python 0.40.0":              "anthropic-sdk",
		"OpenAI/JS 4.52.0":                     "openai-sdk",
		"curl/8.4.0":                           "curl",
		"Mozilla/5.0 (Macintosh) Safari/605.1": telemetry.Other,
		"":                                     telemetry.Other,
		"my-very-own-script/0.1":               telemetry.Other,
	}
	for ua, want := range cases {
		if got := ClientFamily(ua); got != want {
			t.Errorf("ClientFamily(%q) = %q, want %q", ua, got, want)
		}
	}
	for _, m := range clientMarkers {
		if !slices.Contains(telemetry.KnownClients, m.family) {
			t.Errorf("marker %q maps to %q, which KnownClients does not list", m.token, m.family)
		}
	}
}
