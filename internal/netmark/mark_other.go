//go:build !linux

package netmark

import "syscall"

// Control does nothing off Linux. SO_MARK is a Linux socket option and the
// firewall backends this daemon drives do not exist anywhere else; this file
// is here so the packages that use it still build on a developer's machine.
func Control(int) func(network, address string, c syscall.RawConn) error {
	return nil
}
