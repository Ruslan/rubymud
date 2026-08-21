//go:build e2e

// Command e2e-five-room-mud runs the loopback-only deterministic TCP fixture.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"rubymud/go/internal/e2e/fiveroom"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "loopback listen address")
	trace := flag.String("trace", "", "JSONL command trace path")
	darkRoom := flag.Int("dark-room", 0, "room id hidden until the client sends torch")
	roomBoundary := flag.Bool("room-boundary", false, "append the RMUD blank boundary after each room")
	oneWayHillo := flag.Bool("one-way-hillo", false, "replay the one-way Hillo room pair")
	flag.Parse()
	if *trace == "" {
		fatal(fmt.Errorf("--trace is required"))
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		fatal(fmt.Errorf("--listen must be loopback host:port"))
	}
	server, err := fiveroom.ListenWithOptions(*listen, *trace, fiveroom.Options{
		DarkRoom: *darkRoom, RoomBoundary: *roomBoundary, OneWayHillo: *oneWayHillo,
	})
	if err != nil {
		fatal(err)
	}
	addr := server.Addr().(*net.TCPAddr)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true, "host": "127.0.0.1", "port": addr.Port}); err != nil {
		_ = server.Close()
		fatal(err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	if err := server.Close(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
