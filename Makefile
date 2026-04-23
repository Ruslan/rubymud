GO_DIR := go
UI_DIR := ui
BIN_DIR := bin
BIN_NAME := mudhost
MUD ?= 127.0.0.1:4000
LISTEN ?= :8080
DB_PATH ?= data/mudhost.db

.PHONY: help run build test tidy db-init db-schema ui

help:
	@printf "Targets:\n"
	@printf "  make run MUD=host:port [LISTEN=:8080]   Build ui, build and run mudhost\n"
	@printf "  make build                               Build ui and $(BIN_DIR)/$(BIN_NAME)\n"
	@printf "  make ui-install                         Install frontend dependencies\n"
	@printf "  make ui                                  Build frontend assets\n"
	@printf "  make test                                Run Go tests\n"
	@printf "  make tidy                                Run go mod tidy\n"
	@printf "  make db-init                             Initialize or update database schema\n"
	@printf "  make db-schema                           Print mudhost.db schema\n"

.PHONY: ui-install

ui-install:
	cd "$(UI_DIR)" && npm install

ui:
	cd "$(UI_DIR)" && npm run build

run: build
	"./$(BIN_DIR)/$(BIN_NAME)" --mud "$(MUD)" --listen "$(LISTEN)" --db "$(DB_PATH)"

build: ui
	mkdir -p "$(BIN_DIR)"
	cd "$(GO_DIR)" && go build -o "../$(BIN_DIR)/$(BIN_NAME)" ./cmd/mudhost

test:
	cd "$(GO_DIR)" && go test ./...

tidy:
	cd "$(GO_DIR)" && go mod tidy

db-init: build
	"./$(BIN_DIR)/$(BIN_NAME)" --db "$(DB_PATH)" --migrate-only

db-schema:
	sqlite3 "$(DB_PATH)" ".schema"
