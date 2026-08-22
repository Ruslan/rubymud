//go:build e2e

// Command e2e-mapper-fixture seeds an isolated live-E2E SQLite database.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"rubymud/go/internal/e2e/mapperfixture"
)

func main() {
	runDir := flag.String("run-dir", "", "isolated E2E run directory")
	dbPath := flag.String("db", "", "SQLite path inside run-dir")
	mudHost := flag.String("mud-host", "127.0.0.1", "test MUD loopback host")
	mudPort := flag.Int("mud-port", 0, "test MUD port")
	autorecord := flag.Bool("autorecord", false, "seed only the anchor room for the continuous autorecord E2E")
	oneWayHillo := flag.Bool("one-way-hillo", false, "seed the one-way Hillo room pair")
	dynamicCrate := flag.Bool("dynamic-crate", false, "seed the Hillo mutable-exit crate rooms")
	flag.Parse()
	if *runDir == "" || *dbPath == "" {
		fatal(fmt.Errorf("--run-dir and --db are required"))
	}
	result, err := mapperfixture.Seed(mapperfixture.Options{
		RunDir: *runDir, DBPath: *dbPath, MUDHost: *mudHost, MUDPort: *mudPort,
		Autorecord: *autorecord, OneWayHillo: *oneWayHillo, DynamicCrate: *dynamicCrate,
	})
	if err != nil {
		fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
