package telemetry

import _ "embed"

// ExampleHeartbeatsJSON is one batch of heartbeats, on the wire, exactly as an
// agent sends it.
//
// It is embedded rather than left as a file so that both sides of the contract
// can test against the same bytes without knowing where the other one's source
// tree is. The agent's own test regenerates this file and fails when the encoding
// changes; the backend decodes it and feeds it through its handlers. So a field
// renamed on one side breaks a test on the other, which is the drift that
// otherwise has no symptom at all — a dashboard column quietly always zero.
//
// The timestamps in it are fixed, and a consumer that cares about freshness has
// to re-stamp them. That is deliberate: a golden file built from time.Now() pins
// nothing.
//
//go:embed testdata/heartbeats.json
var ExampleHeartbeatsJSON string
