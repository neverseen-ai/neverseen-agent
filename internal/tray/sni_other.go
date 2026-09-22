//go:build !linux

package tray

// whyNoIcon has nothing to answer away from Linux.
//
// macOS and Windows draw an icon because the platform itself has somewhere to put
// one; there is no host to be missing. Returning "" rather than build-tagging the
// call site keeps Run one function on every platform — a Run that existed in three
// versions is three places for the polling loop to drift.
func whyNoIcon() string { return "" }
