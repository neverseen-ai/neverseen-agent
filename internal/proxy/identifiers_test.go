package proxy

import (
	"strings"
	"testing"
)

// The three identifiers, exactly as Claude Code writes them: twice over, once in
// `metadata.user_id` and once inside the arguments a tool was called with. A body
// holding only one of the two would pass over an exemption that reached the
// metadata field and left the prompt tokenised, which is the half this is for.
const (
	sessionID   = "18af2c2b-f1a0-4001-ab0b-77a9b65dce94"
	deviceID    = "5a1c93ee2bdd44c500812f004ba523ac197dbe50d9a18a51f266fd4e76958db7"
	accountUUID = "841ad98d-000d-4ae5-b47d-5ac2edca66a9"

	// A real credential in the same body, so every assertion below has something
	// that must still be replaced beside the thing that must not. Narrowing what
	// an agent masks is the change that leaks, so neither half is meaningful on
	// its own.
	anthropicKey = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
)

// decoded hands a test body to exemptIdentifiers the way maskRequest does: as the
// document, not the bytes. The exemption is read at a path, and a path only exists
// in a decoded document.
func decoded(t *testing.T, body string) any {
	t.Helper()
	doc, err := decodeJSONBody([]byte(body))
	if err != nil {
		t.Fatalf("decode the test body: %v", err)
	}
	return doc
}

const claudeCodeBody = `{"model":"claude-opus-5",` +
	`"messages":[{"role":"user","content":[{"type":"text","text":` +
	`"ARGUMENTS: {\"session_id\":\"` + sessionID + `\"} and the key ` + anthropicKey + `"}]}],` +
	`"metadata":{"user_id":"{\"device_id\":\"` + deviceID + `\",` +
	`\"account_uuid\":\"` + accountUUID + `\",\"session_id\":\"` + sessionID + `\"}"}}`

func TestAnthropicOwnIdentifiersReachItInClear(t *testing.T) {
	up := newUpstream(t, echoJSON)
	anthropicIs(t, up)
	agent := newAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "s1", claudeCodeBody)

	bodies, _ := up.received()
	sent := bodies[0]

	for _, id := range []struct{ name, value string }{
		{"session_id", sessionID},
		{"device_id", deviceID},
		{"account_uuid", accountUUID},
	} {
		if !strings.Contains(sent, id.value) {
			t.Errorf("%s did not reach anthropic in clear:\n%s", id.name, sent)
		}
	}
	// Both occurrences of the session id, not just the metadata one: the field and
	// the prompt have to name the same client.
	if got := strings.Count(sent, sessionID); got != 2 {
		t.Errorf("the session id reached the provider %d times, want 2:\n%s", got, sent)
	}

	if strings.Contains(sent, anthropicKey) {
		t.Errorf("the API key in the same body was forwarded in clear:\n%s", sent)
	}
}

func TestAnotherProviderStillMasksTheSameIdentifiers(t *testing.T) {
	up := newUpstream(t, echoJSON)
	anthropicIs(t, up)
	agent := newAgent(t, up, []string{"fr"})

	post(t, agent, "/openai/v1/messages", "s1", claudeCodeBody)

	bodies, _ := up.received()
	sent := bodies[0]

	// Anthropic issued these; a vendor that did not has no business reading them.
	if strings.Contains(sent, sessionID) {
		t.Errorf("the session id reached another provider in clear:\n%s", sent)
	}
	if strings.Contains(sent, anthropicKey) {
		t.Errorf("the API key was forwarded in clear:\n%s", sent)
	}
}

// The route named "anthropic" pointed at another host — the override
// .env.example documents — is another host, and the identifiers stay masked on
// the way to it. Keyed on the code, this test's body went to the gateway in clear.
func TestARouteNamedAnthropicPointedElsewhereEarnsNoExemption(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"}) // no anthropicIs: the upstream is a gateway

	post(t, agent, "/anthropic/v1/messages", "s1", claudeCodeBody)

	bodies, _ := up.received()
	if strings.Contains(bodies[0], sessionID) {
		t.Errorf("the session id reached a host that is not Anthropic in clear:\n%s", bodies[0])
	}
}

// A field name alone cannot earn an exemption, and the shape is what says so.
//
// Written inside metadata.user_id, which is the one place the exemption is read
// from: everything about this value except its shape says "exempt me". A rule that
// trusted the field would forward an Anthropic key straight to Anthropic in clear,
// which is the whole reason identifierValue is narrow.
func TestAnIdentifierFieldDoesNotExemptACredential(t *testing.T) {
	up := newUpstream(t, echoJSON)
	anthropicIs(t, up)
	agent := newAgent(t, up, []string{"fr"})

	body := `{"metadata":{"user_id":"{\"session_id\":\"` + anthropicKey + `\"}"}}`
	post(t, agent, "/anthropic/v1/messages", "s1", body)

	bodies, _ := up.received()
	if strings.Contains(bodies[0], anthropicKey) {
		t.Errorf("a credential behind session_id was exempted:\n%s", bodies[0])
	}
}

func TestExemptIdentifiersReadsTheClientsOwnField(t *testing.T) {
	// The nested document, escaped as it reads on the wire: that is where the three
	// live, and reading them there is what makes the exemption Anthropic's rather
	// than anybody's who can type a field name.
	exempt := exemptIdentifiers(identifierHost, decoded(t, claudeCodeBody))
	for _, value := range []string{sessionID, deviceID, accountUUID} {
		if !exempt[value] {
			t.Errorf("%q was not exempted", value)
		}
	}

	if exemptIdentifiers("api.openai.com", decoded(t, claudeCodeBody)) != nil {
		t.Error("another provider was given an exemption")
	}
}

// A `session_id` outside metadata.user_id is the caller's, not Anthropic's.
//
// Read over the whole body, the rule exempted whatever sat behind one of the three
// names anywhere in it — so a .env or a log line pasted into a prompt had its own
// session token written back in clear, uncounted and unminted, to a provider that
// had no business seeing it. It is masked today, and narrowing what an agent masks
// is the change that leaks.
func TestAPastedSessionIdIsNotTheClientsIdentifier(t *testing.T) {
	// The shape identifierValue accepts, so nothing but where it was found separates
	// this from the identifier in the metadata field.
	const pasted = "0123456789abcdef0123456789abcdef"

	body := `{"messages":[{"content":"SESSION_ID=` + pasted + `"}],` +
		`"metadata":{"user_id":"{\"session_id\":\"` + sessionID + `\"}"}}`

	exempt := exemptIdentifiers(identifierHost, decoded(t, body))
	if exempt[pasted] {
		t.Error("a session id pasted into a prompt was exempted")
	}
	if !exempt[sessionID] {
		t.Error("the identifier the client declared in metadata.user_id was not exempted")
	}

	up := newUpstream(t, echoJSON)
	anthropicIs(t, up)
	agent := newAgent(t, up, []string{"fr"})
	post(t, agent, "/anthropic/v1/messages", "s1", body)

	bodies, _ := up.received()
	if strings.Contains(bodies[0], pasted) {
		t.Errorf("the pasted session id reached the provider in clear:\n%s", bodies[0])
	}
}

// The anchor is the *path* metadata.user_id, not the field name. Matched over the
// raw bytes, any member spelled `user_id` earned the exemption — and a body carries
// structured JSON beyond metadata: the `input` of every tool call in the history.
// A tool called with `{"user_id":"{\"session_id\":\"…\"}"}` exempted whatever hex
// sat inside it body-wide, so a 32-hex credential elsewhere in the same exchange
// went to the provider in clear, uncounted and unminted.
func TestAUserIDInAToolCallEarnsNoExemption(t *testing.T) {
	const pasted = "0123456789abcdef0123456789abcdef"

	body := `{"messages":[` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"lookup",` +
		`"input":{"user_id":"{\"session_id\":\"` + pasted + `\"}"}}]},` +
		`{"role":"user","content":"and the .env holds SESSION_ID=` + pasted + `"}],` +
		`"metadata":{"user_id":"{\"session_id\":\"` + sessionID + `\"}"}}`

	exempt := exemptIdentifiers(identifierHost, decoded(t, body))
	if exempt[pasted] {
		t.Error("a user_id inside a tool call's input was exempted")
	}
	if !exempt[sessionID] {
		t.Error("the identifier the client declared in metadata.user_id was not exempted")
	}
}

func TestConversationOfReadsTheClientsSessionOnlyWhereItMay(t *testing.T) {
	doc, err := decodeJSONBody([]byte(`{"metadata":{"user_id":"{\"device_id\":\"5a1c\",\"session_id\":\"18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b\"}"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationOf("default", identifierHost, doc); got != "18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b" {
		t.Errorf("a header-less request to Anthropic is conversation %q, want the client's session id", got)
	}
	// A header names the session, and the header wins: it is what the mapping is
	// scoped by.
	if got := conversationOf("conv-7", identifierHost, doc); got != "conv-7" {
		t.Errorf("a request with a session header is conversation %q, want conv-7", got)
	}
	// Another host has no claim on the identifier.
	if got := conversationOf("default", "gateway.internal", doc); got != "default" {
		t.Errorf("a request to another host is conversation %q, want default", got)
	}
	// A value that is not the shape of an issued id is not read as one.
	odd, _ := decodeJSONBody([]byte(`{"metadata":{"user_id":"{\"session_id\":\"sk-ant-api03-notanid\"}"}}`))
	if got := conversationOf("default", identifierHost, odd); got != "default" {
		t.Errorf("a credential-shaped session_id was read as a conversation: %q", got)
	}
}
