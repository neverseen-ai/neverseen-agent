//go:build !linux

package proxy

// accountLookup reports that this platform cannot tell accounts apart yet — see the
// TODO in peer.go — so neither the agent nor its surfaces ask.
func accountLookup() (ownerLookup, uint32) {
	return nil, 0
}
