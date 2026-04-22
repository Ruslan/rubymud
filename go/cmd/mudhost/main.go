package main

import (
	"flag"
	"log"
	"net"
	"strconv"

	"rubymud/go/internal/config"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
	"rubymud/go/internal/web"
)

func main() {
	mudAddr := flag.String("mud", "", "MUD server address in host:port format")
	listenAddr := flag.String("listen", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "../data/mudhost.db", "SQLite database path")
	configPath := flag.String("config", "../config.rb", "Config file path for hotkeys")
	flag.Parse()

	if *mudAddr == "" {
		log.Fatal("usage: mudhost --mud HOST:PORT [--listen :8080] [--db ../data/mudhost.db]")
	}

	host, port, err := splitMudAddr(*mudAddr)
	if err != nil {
		log.Fatalf("invalid mud address: %v", err)
	}

	store, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	sessionRecord, err := store.EnsureDefaultSession(host, port)
	if err != nil {
		log.Fatalf("ensure default session: %v", err)
	}

	hotkeys, err := config.LoadHotkeys(*configPath)
	if err != nil {
		log.Printf("load hotkeys warning: %v", err)
	}

	v := vm.New(store, sessionRecord.ID)
	if err := v.Reload(); err != nil {
		log.Fatalf("vm reload: %v", err)
	}
	log.Printf("loaded %d aliases, %d variables, %d triggers, %d highlights", len(v.Aliases()), len(v.Variables()), len(v.Triggers()), len(v.Highlights()))

	sess, err := session.New(sessionRecord.ID, *mudAddr, store, v)
	if err != nil {
		log.Fatalf("connect to MUD: %v", err)
	}
	defer sess.Close()

	go sess.RunReadLoop()

	server := web.New(*listenAddr, sess, hotkeys)
	log.Printf("mudhost listening on %s and connected to %s using db %s", *listenAddr, *mudAddr, *dbPath)
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
