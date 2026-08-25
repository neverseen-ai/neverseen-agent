package proxy

import (
	"net"
	"sort"
)

// maxAddresses bounds what is reported.
//
// A workstation has one or two useful addresses. A machine with Docker, a VPN and
// a couple of virtual bridges has a dozen, and reporting all of them would fill
// the fleet view with noise that hides the one address a person could recognise.
const maxAddresses = 4

// localAddresses returns the machine's own routable IP addresses.
//
// What is excluded is as deliberate as what is kept, because the purpose is to let
// somebody recognise a machine — not to inventory its network stack:
//
//   - loopback: every machine has 127.0.0.1, so it distinguishes nothing;
//   - link-local (169.254/16, fe80::/10): assigned when DHCP failed, so it names a
//     broken network rather than a machine;
//   - interfaces that are down, which are addresses the machine is not reachable
//     at;
//   - IPv6 temporary privacy addresses cannot be told apart from stable ones here,
//     so IPv6 is reported as-is and may churn. IPv4 first in the ordering, because
//     it is the one an operator recognises.
//
// Errors are swallowed and an empty list returned. An agent that refused to start
// because it could not enumerate its interfaces would be a masking proxy taken
// down by a reporting detail, and the backend records the address it observes the
// connection from regardless.
func localAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var four, six []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if ip.To4() != nil {
				four = append(four, ip.String())
			} else {
				six = append(six, ip.String())
			}
		}
	}

	// Sorted so the same machine reports the same list in the same order every
	// time. Interface enumeration order is not guaranteed, and a list that
	// reshuffled would look like a machine whose addresses kept changing.
	sort.Strings(four)
	sort.Strings(six)

	// Built into its own slice rather than appended onto four: appending would be
	// free to reuse four's backing array, and a helper that quietly mutates one of
	// its own locals is the kind of thing that is fine until somebody reads four
	// again below it.
	all := make([]string, 0, len(four)+len(six))
	all = append(all, four...)
	all = append(all, six...)

	if len(all) > maxAddresses {
		all = all[:maxAddresses]
	}
	return all
}
