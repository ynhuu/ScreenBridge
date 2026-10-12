// Package anet provides the interface discovery helpers consumed by Pion.
// The upstream Android implementation links to net.zoneCache, an unexported
// Go runtime symbol that newer Go toolchains reject. Standard-library
// enumeration works for this root-run Android module and avoids that linkage.
package anet

import "net"

func Interfaces() ([]net.Interface, error) { return net.Interfaces() }

func InterfaceAddrsByInterface(iface *net.Interface) ([]net.Addr, error) {
	if iface == nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Err: net.InvalidAddrError("nil interface")}
	}
	return iface.Addrs()
}
