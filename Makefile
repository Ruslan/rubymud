GO_DIR := go
UI_DIR := ui
BIN_DIR := bin
BIN_NAME := mudhost
MUD ?= 127.0.0.1:4000
LISTEN ?= :8080
DB_PATH ?= data/mudhost.db
MIGRATION_DIR := go/sqlite/migrations

.PHONY: help run build test tidy db-init db-schema ui

help:
	@printf "Targets:\n"
	@printf "  make run MUD=host:port [LISTEN=:8080]   Init db, build ui, and run mudhost\n"
	@printf "  make build                               Build ui and $(BIN_DIR)/$(BIN_NAME)\n"
	@printf "  make ui-install                         Install frontend dependencies\n"
	@printf "  make ui                                  Build frontend assets\n"
	@printf "  make test                                Run Go tests\n"
	@printf "  make tidy                                Run go mod tidy\n"
	@printf "  make db-init                             Create or update mudhost.db schema\n"
	@printf "  make db-schema                           Print mudhost.db schema\n"

.PHONY: ui-install

ui-install:
	cd "$(UI_DIR)" && npm install

ui:
	cd "$(UI_DIR)" && npm run build

run: db-init build
	"./$(BIN_DIR)/$(BIN_NAME)" --mud "$(MUD)" --listen "$(LISTEN)" --db "$(DB_PATH)"

build: ui
	mkdir -p "$(BIN_DIR)"
	cd "$(GO_DIR)" && go build -o "../$(BIN_DIR)/$(BIN_NAME)" ./cmd/mudhost

test:
	cd "$(GO_DIR)" && go test ./...

tidy:
	cd "$(GO_DIR)" && go mod tidy

db-init:
	mkdir -p "$(dir $(DB_PATH))"
	for f in $(MIGRATION_DIR)/*.sql; do sqlite3 "$(DB_PATH)" < "$$f"; done

db-schema:
	sqlite3 "$(DB_PATH)" ".schema"
