package main

import (
	"flag"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/web"
)

func main() {
	mudAddr := flag.String("mud", "", "Optional: initial MUD server address in host:port format")
	listenAddr := flag.String("listen", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "data/mudhost.db", "SQLite database path")
	configDirPath := flag.String("config-dir", "", "Directory for exported profile .tt files (default: config/ next to --db)")
	flag.Parse()

	// Ensure data directory exists
	dataDir := filepath.Dir(*dbPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("failed to create data directory %s: %v", dataDir, err)
	}

	// Default config-dir is sibling of the DB file
	if *configDirPath == "" {
		*configDirPath = filepath.Join(dataDir, "config")
	}

	// Ensure config directory exists
	if err := os.MkdirAll(*configDirPath, 0755); err != nil {
		log.Fatalf("failed to create config directory %s: %v", *configDirPath, err)
	}

	store, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	manager := session.NewManager(store)

	// If --mud is provided, ensure a default session exists and connect to it
	if *mudAddr != "" {
		host, port, err := splitMudAddr(*mudAddr)
		if err != nil {
			log.Fatalf("invalid mud address: %v", err)
		}

		record, err := store.EnsureDefaultSession(host, port)
		if err != nil {
			log.Fatalf("ensure default session: %v", err)
		}

		if _, err := manager.Connect(record.ID); err != nil {
			log.Printf("failed to connect to initial mud session: %v", err)
		}
	}

	server := web.New(*listenAddr, manager, store, *configDirPath)
	log.Printf("mudhost listening on %s using db %s", *listenAddr, *dbPath)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("web server failed: %v", err)
	}
}

func splitMudAddr(mudAddr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(mudAddr)
	if err != nil {
		return "", 0, err
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}

	return host, port, nil
}
