package proxy

import (
	"encoding/binary"
	"net/netip"
	"os"
)

// accountLookup is how this platform tells accounts apart, and the account this
// process runs as.
func accountLookup() (ownerLookup, uint32) {
	return procNetOwner, uint32(os.Getuid())
}

// procNetOwner reads the kernel's socket tables for the owner of one socket.
//
// IPv4 first for an IPv4 address, then IPv6, where a dual-stack listener files the
// same connection under its mapped form.
//
// A table that cannot be read leaves the socket unknown, and an unknown socket is
// refused: loudly, on every request, which is the failure somebody notices.
//
// TODO: the tables are read whole for every new connection (once, through
// Server.ConnContext), which is linear in the sockets on the machine — milliseconds on a
// server holding tens of thousands. The upgrade is to ask sock_diag over netlink for the
// one socket.
func procNetOwner(local, remote netip.AddrPort) (uint32, bool) {
	tables := []string{"/proc/net/tcp6"}
	if local.Addr().Unmap().Is4() {
		tables = []string{"/proc/net/tcp", "/proc/net/tcp6"}
	}
	for _, path := range tables {
		table, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if uid, ok := ownerIn(string(table), local, remote, binary.NativeEndian); ok {
			return uid, true
		}
	}
	return 0, false
}
