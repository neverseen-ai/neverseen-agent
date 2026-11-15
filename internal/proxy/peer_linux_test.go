package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// Through real sockets and the kernel's own tables. A lookup that misses refuses, so
// the failure this guards against is not a leak but every request on a Linux
// workstation refused — the owner's included.
func TestTheKernelNamesThisAccountAtBothEnds(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			addr := map[string]string{"tcp4": "127.0.0.1:0", "tcp6": "[::1]:0"}[network]
			ln, err := net.Listen(network, addr)
			if err != nil {
				t.Skipf("no %s loopback here: %v", network, err)
			}
			t.Cleanup(func() { _ = ln.Close() })

			conn, err := net.Dial(network, ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })

			own := uint32(os.Getuid())
			us := netip.MustParseAddrPort(conn.LocalAddr().String())
			agent := netip.MustParseAddrPort(conn.RemoteAddr().String())

			if uid, ok := procNetOwner(us, agent); !ok || uid != own {
				t.Errorf("the caller's socket: uid %d, %v; want %d", uid, ok, own)
			}
			// Not yet accepted: before Linux 6.17 the kernel names root as its owner, so it
			// is its listener's — this account's.
			if uid, ok := procNetOwner(agent, us); !ok || uid != own {
				t.Errorf("the agent's socket: uid %d, %v; want %d", uid, ok, own)
			}
		})
	}
}

// Both halves at once, as a surface reaches the agent: agentClient asks who listens,
// guardAccount asks who calls, and the owner must pass both.
//
// Wired as the agent is, through ConnContext: the decision taken when the connection is
// accepted is the one the real tables must get right. Every other test serving the
// handler on Linux goes through the per-request half.
func TestTheOwnersOwnRequestsPass(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	if srv.ownerOf == nil {
		t.Fatal("an agent on Linux cannot tell accounts apart")
	}
	agent := httptest.NewUnstartedServer(srv.Handler())
	agent.Config.ConnContext = srv.ConnContext
	agent.Start()
	t.Cleanup(agent.Close)

	status := Query(t.Context(), strings.TrimPrefix(agent.URL, "http://"), 2*time.Second)
	if !status.Answering || status.Foreign != nil {
		t.Fatalf("Answering %v, Foreign %v: the owner's own request was refused", status.Answering, status.Foreign)
	}

	resp, err := http.Get(agent.URL + "/anthropic/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("the owner's provider request was refused for its account")
	}
}
