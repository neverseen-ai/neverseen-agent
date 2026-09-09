//go:build !windows

package proxy

// HideConsole does nothing off Windows.
//
// The flag that calls it is accepted everywhere rather than hidden behind a build
// tag, so that `neverseen proxy --detach` fails the same way on every platform —
// which is to say it does not fail at all. A flag that exists on one platform is a
// service definition that cannot be rendered on another, and rendering every
// platform's definition anywhere is what internal/service is built around.
func HideConsole() {}
