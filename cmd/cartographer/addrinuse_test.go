package main

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// serve on a taken port names the address, says another process holds it and
// gives both ways out, instead of the raw bind error (#1998). The busy port is
// a real one, so the platform's own error number is what isAddrInUse reads.
func TestServeHTTPBusyPortSaysWhatToDo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	boot := func(func(string)) kbBootstrap { return kbBootstrap{} }
	err = serveHTTP(addr, boot, config.AuthConfig{Mode: "off"}, nil, false, nil, nil, nil)
	if err == nil {
		t.Fatal("serveHTTP on a taken port returned nil")
	}
	msg := err.Error()
	for _, want := range []string{addr, "already in use by another process", "stop that process", "change the listen address/port"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "bind:") {
		t.Errorf("the raw bind error leaked into the message:\n%s", msg)
	}
}

func TestIsAddrInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, busy := net.Listen("tcp", ln.Addr().String())
	if busy == nil {
		t.Fatal("a second listen on the same address succeeded")
	}
	if !isAddrInUse(busy) {
		t.Errorf("isAddrInUse(%v) = false", busy)
	}
	if isAddrInUse(errors.New("permission denied")) {
		t.Error("an unrelated error read as address in use")
	}
}

// Any other bind failure keeps its own text, with the address in front.
func TestListenErrorOtherFailureKeepsItsCause(t *testing.T) {
	cause := errors.New("boom")
	err := listenError(":39273", cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "port 39273 (:39273)") {
		t.Errorf("listenError = %v", err)
	}
	if strings.Contains(err.Error(), "already in use") {
		t.Errorf("a failure that is not a busy port says it is: %v", err)
	}
}
