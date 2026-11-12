package main

import (
	"fmt"
	"net"
)

// listenError turns the error of binding the HTTP listener into one an
// operator can act on (#1998). A busy address names itself, says another
// process holds it and gives the two ways out; any other failure keeps its
// own text, only prefixed with the address.
func listenError(addr string, err error) error {
	if isAddrInUse(err) {
		return fmt.Errorf("cannot listen on %s: the address is already in use by another process (it may be another cartographer serve, or an unrelated program): stop that process, or change the listen address/port (http: in the server config, or --http)", describeAddr(addr))
	}
	return fmt.Errorf("cannot listen on %s: %w", describeAddr(addr), err)
}

// describeAddr is addr as it reads in a message: a bare port (":39273") names
// the port on every interface.
func describeAddr(addr string) string {
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		return "port " + port + " (" + addr + ")"
	}
	return addr
}
