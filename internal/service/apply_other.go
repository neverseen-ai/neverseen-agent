//go:build !darwin && !linux && !windows

package service

import "fmt"

// The agent builds anywhere Go does, and on the platforms without a service manager
// this package knows, it says so rather than registering nothing and reporting
// success — which is how somebody ends up believing their traffic is masked.

func Install(l Layout) error   { return unsupported(l) }
func Uninstall(l Layout) error { return unsupported(l) }
func Restart(l Layout) error   { return unsupported(l) }

func unsupported(l Layout) error {
	return fmt.Errorf("no service manager known for %s; run `neverseen proxy` yourself", l.Platform)
}
