package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// Against a real agent rather than a fake one, because what is under test is that the
// printed link is one the real route honours: a fake would agree with whatever this
// command assumed.
func TestSettingsPrintsALinkThatOpensThePageWithTheKey(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".neverseen"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".neverseen", "control.key"),
		[]byte(sampleKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := proxy.New(proxy.Config{ControlKey: sampleKey}, detector.New(detector.Config{}), v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	t.Setenv(proxy.EnvListen, strings.TrimPrefix(agent.URL, "http://"))

	var out bytes.Buffer
	if err := run([]string{"console"}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("console: %v", err)
	}
	// The name the command had before: a note written then still opens the page.
	if err := run([]string{"settings"}, strings.NewReader(""), io.Discard); err != nil {
		t.Errorf("the command's earlier name, settings: %v", err)
	}

	var link string
	for _, field := range strings.Fields(out.String()) {
		if strings.HasPrefix(field, agent.URL+"/settings?") {
			link = field
		}
	}
	if link == "" {
		t.Fatalf("no link to the settings page was printed: %q", out.String())
	}

	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), sampleKey) {
		t.Fatal("the printed link opened the page without the key")
	}
}

// With no key there is no way to prove who is asking, so nothing is printed and the
// message names the file it looked in.
func TestSettingsWithoutAKeyPrintsNoLink(t *testing.T) {
	testHome(t)

	var out bytes.Buffer
	err := run([]string{"console"}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("a link was printed with no control key to ask for it")
	}
	if !strings.Contains(err.Error(), "control.key") {
		t.Errorf("the message does not say where the key was looked for: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("something was printed to stdout: %q", out.String())
	}
}
