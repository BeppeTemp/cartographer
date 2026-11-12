package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/wsl"
)

// cmdWsl is internal, not in the usage table: the Windows keep-alive task runs
// `cartographer wsl keepalive` inside the distro.
func cmdWsl(args []string) int {
	if len(args) != 1 || args[0] != "keepalive" {
		fmt.Fprintln(os.Stderr, "usage: cartographer wsl keepalive   (internal: run by the Windows task that keeps the WSL distro alive)")
		return exitStatusError
	}
	if !wslDetectFn().Is {
		fmt.Fprintln(os.Stderr, "Error: `cartographer wsl keepalive` runs only inside a WSL distro")
		return exitStatusError
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(stop)
	tick := time.NewTicker(keepAliveEvery)
	defer tick.Stop()
	runKeepAlive(stop, tick.C, service.NewManager().KeepAliveEnsure)
	return 0
}

// wslDetectFn and keepAliveEvery are test seams.
var (
	wslDetectFn    = wsl.Detect
	keepAliveEvery = 60 * time.Second
)

// runKeepAlive holds the process (and so the distro) up until a signal, and
// runs ensure at start and on every tick. ensure is the cron backend's Ensure:
// WSL starts no cron daemon at distro boot, so nothing else would bring a cron
// server back after `wsl --shutdown`. An error is logged and never ends the
// loop: a keep-alive that exits lets the distro stop.
func runKeepAlive(stop <-chan os.Signal, tick <-chan time.Time, ensure func() error) {
	step := func() {
		if err := ensure(); err != nil {
			fmt.Fprintln(os.Stderr, "keepalive:", err)
		}
	}
	step()
	for {
		select {
		case <-stop:
			return
		case <-tick:
			step()
		}
	}
}
