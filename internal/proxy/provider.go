package proxy

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// A provider is an upstream the agent forwards to, addressed by the first
// segment of the path: a caller pointing its SDK at
// "http://localhost:8787/anthropic" reaches api.anthropic.com, and one pointing
// at ".../openai" reaches api.openai.com.
//
// Routing on an explicit segment rather than guessing from the path is what makes
// eight providers work on one port. The alternative does not: six of these speak
// the same OpenAI-compatible path, so "/v1/chat/completions" names no upstream at
// all.
//
// The agent holds no API keys. Whatever credential the caller sent — a bearer
// token, an x-api-key header — is forwarded untouched, because the tool making
// the request already has it. That is the honest reading of "bring your own key",
// and it means a workstation agent is not one more place a key is stored.

// Provider is one upstream.
type Provider struct {
	// Code is the path segment that selects it.
	Code string
	// BaseURL is where its requests go.
	BaseURL string
}

// DefaultProviders is the set the agent knows without being configured.
var DefaultProviders = []Provider{
	{Code: "anthropic", BaseURL: "https://api.anthropic.com"},
	{Code: "openai", BaseURL: "https://api.openai.com"},
	{Code: "gemini", BaseURL: "https://generativelanguage.googleapis.com"},
	{Code: "mistral", BaseURL: "https://api.mistral.ai"},
	{Code: "groq", BaseURL: "https://api.groq.com"},
	{Code: "together", BaseURL: "https://api.together.xyz"},
	{Code: "deepinfra", BaseURL: "https://api.deepinfra.com"},
	{Code: "xai", BaseURL: "https://api.x.ai"},
}

// ParseProviders reads provider overrides in "code=url" form, separated by
// commas, and returns the default set with those applied.
//
// An override rather than a replacement: a deployment that points one provider at
// a gateway of its own should not lose the other seven, and a configuration that
// silently dropped them would look like the agent had stopped supporting them.
func ParseProviders(spec string) ([]Provider, error) {
	providers := make([]Provider, len(DefaultProviders))
	copy(providers, DefaultProviders)

	if strings.TrimSpace(spec) == "" {
		return providers, nil
	}

	for _, entry := range strings.Split(spec, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}

		code, raw, found := strings.Cut(entry, "=")
		code, raw = strings.ToLower(strings.TrimSpace(code)), strings.TrimSpace(raw)
		if !found || code == "" || raw == "" {
			return nil, fmt.Errorf("provider override %q is not in code=url form", entry)
		}
		if err := validateBaseURL(raw); err != nil {
			return nil, fmt.Errorf("provider %q: %w", code, err)
		}

		replaced := false
		for i := range providers {
			if providers[i].Code == code {
				providers[i].BaseURL = raw
				replaced = true
				break
			}
		}
		if !replaced {
			providers = append(providers, Provider{Code: code, BaseURL: raw})
		}
	}
	return providers, nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q has scheme %q, want http or https", raw, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	return nil
}

// splitProvider takes the provider code off the front of a path and returns it
// with what remains, which is what the upstream should receive.
//
//	/anthropic/v1/messages -> "anthropic", "/v1/messages"
//	/anthropic             -> "anthropic", "/"
func splitProvider(path string) (code, rest string) {
	trimmed := strings.TrimPrefix(path, "/")
	code, rest, found := strings.Cut(trimmed, "/")
	if !found {
		return code, "/"
	}
	return code, "/" + rest
}

// providerCodes lists the configured codes, for the error a caller gets when it
// names one that does not exist. Built from the configuration rather than from a
// copy, so it cannot go stale.
func providerCodes(providers []Provider) []string {
	codes := make([]string, 0, len(providers))
	for _, p := range providers {
		codes = append(codes, p.Code)
	}
	sort.Strings(codes)
	return codes
}

// Providers reports the upstreams this server serves, for the line the command
// prints on start-up.
func (s *Server) Providers() []string { return providerCodes(s.providers) }
