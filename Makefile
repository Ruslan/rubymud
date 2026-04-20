GO_DIR := go
MUD ?= 127.0.0.1:4000
LISTEN ?= :8080
DB_PATH ?= data/mudhost.db
DB_INIT_SQL := go/sqlite/migrations/001_init.sql

.PHONY: help run build tidy db-init db-schema

help:
	@printf "Targets:\n"
	@printf "  make run MUD=host:port [LISTEN=:8080]   Init db, build and run mudhost\n"
	@printf "  make build                               Build mudhost binary\n"
	@printf "  make tidy                                Run go mod tidy\n"
	@printf "  make db-init                             Create or update mudhost.db schema\n"
	@printf "  make db-schema                           Print mudhost.db schema\n"

run: db-init
	cd "$(GO_DIR)" && go run ./cmd/mudhost --mud "$(MUD)" --listen "$(LISTEN)" --db "../$(DB_PATH)"

build:
	cd "$(GO_DIR)" && go build ./cmd/mudhost

tidy:
	cd "$(GO_DIR)" && go mod tidy

db-init:
	mkdir -p "$(dir $(DB_PATH))"
	sqlite3 "$(DB_PATH)" < "$(DB_INIT_SQL)"

db-schema:
	sqlite3 "$(DB_PATH)" ".schema"
