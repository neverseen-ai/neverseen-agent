package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// askRTK calls GET /rtk with the key, on a server reading rtk through the given
// reader.
func askRTK(t *testing.T, reader rtkReader) rtkReply {
	t.Helper()
	srv := settingsAgent(t, testControlKey)
	srv.rtk = reader

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rtk", nil)
	req.RemoteAddr = "127.0.0.1:52341"
	req.Host = "localhost:9787"
	req.Header.Set(controlHeader, testControlKey)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /rtk answered %d: %s", rec.Code, rec.Body)
	}
	var reply rtkReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("GET /rtk is not the reply's shape: %v\n%s", err, rec.Body)
	}
	return reply
}

func readerOf(out string, err error) rtkReader {
	return rtkReader{
		path: func() string { return "/opt/rtk" },
		gain: func(context.Context, string) ([]byte, error) { return []byte(out), err },
	}
}

// The page draws from the rows, so they travel in the agent's shape: the three
// counts of a day, and nothing else rtk prints in a row or beside them.
func TestRTKServesTheDailyRows(t *testing.T) {
	reply := askRTK(t, readerOf(`{"summary":{"total_saved":9},"daily":[
		{"date":"2026-11-16","commands":129,"input_tokens":36416,"output_tokens":24431,"saved_tokens":11985,"avg_time_ms":7},
		{"date":"2026-11-17","commands":121,"input_tokens":43010,"output_tokens":29689}]}`, nil))

	if !reply.Installed || reply.Path != "/opt/rtk" || reply.ReadAt == nil || reply.Problem != "" {
		t.Errorf("a reading is installed, located and dated, with no problem: %+v", reply)
	}
	want := []rtkDayReply{
		{Date: "2026-11-16", Commands: 129, Input: 36416, Output: 24431},
		{Date: "2026-11-17", Commands: 121, Input: 43010, Output: 29689},
	}
	if len(reply.Days) != len(want) || reply.Days[0] != want[0] || reply.Days[1] != want[1] {
		t.Errorf("days = %+v, want %+v", reply.Days, want)
	}
}

// A binary called rtk that gives no reading is installed and broken, said in its own
// words — never an empty record, which the page would draw as ninety idle days.
func TestRTKThatGivesNoReadingIsAProblemNotAnEmptyRecord(t *testing.T) {
	for name, reader := range map[string]rtkReader{
		"it failed":         readerOf("", errors.New("exit status 1: unable to open database file")),
		"it printed no row": readerOf(`{}`, nil),
		"it printed prose":  readerOf(`rtk: Rust Type Kit`, nil),
	} {
		t.Run(name, func(t *testing.T) {
			reply := askRTK(t, reader)
			if !reply.Installed || reply.Problem == "" || len(reply.Days) != 0 {
				t.Errorf("want installed, a problem and no day: %+v", reply)
			}
		})
	}
}

// No rtk — none on the workstation, or a server nothing was wired into, which is
// every server a test builds — answers that and nothing else, and runs nothing. With
// one, the binary run is the binary named: it is looked up once.
func TestRTKAbsentIsSaidAndNothingIsRun(t *testing.T) {
	ran := false
	for name, reader := range map[string]rtkReader{
		"not wired": {},
		"not found": {
			path: func() string { return "" },
			gain: func(context.Context, string) ([]byte, error) { ran = true; return nil, nil },
		},
	} {
		t.Run(name, func(t *testing.T) {
			if reply := askRTK(t, reader); reply.Installed || reply.Path != "" || reply.ReadAt != nil || reply.Days != nil {
				t.Errorf("want an empty answer: %+v", reply)
			}
		})
	}
	if ran {
		t.Error("rtk was run although no binary was found")
	}

	lookups, run := 0, ""
	askRTK(t, rtkReader{
		path: func() string { lookups++; return "/opt/rtk" },
		gain: func(_ context.Context, path string) ([]byte, error) { run = path; return []byte(`{"daily":[]}`), nil },
	})
	if lookups != 1 || run != "/opt/rtk" {
		t.Errorf("rtk was looked up %d times and %q was run, want once and /opt/rtk", lookups, run)
	}
}

// The page chooses what its rtk screen shows on what GET /today says, and that is a
// lookup: a binary found is enough, and nothing is run to say it.
func TestTodaySaysWhetherRTKIsInstalledWithoutRunningIt(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	if srv.agentNow().RTK {
		t.Error("a server nothing was wired into says rtk is installed")
	}
	srv.rtk = rtkReader{
		path: func() string { return "/opt/rtk" },
		gain: func(context.Context, string) ([]byte, error) {
			t.Error("GET /today ran rtk")
			return nil, nil
		},
	}
	if !srv.agentNow().RTK {
		t.Error("a binary was found and /today does not say so")
	}
	out, err := json.Marshal(srv.agentNow())
	if err != nil || !strings.Contains(string(out), `"rtk":true`) {
		t.Errorf("the agent state does not carry rtk: %s (%v)", out, err)
	}
}
