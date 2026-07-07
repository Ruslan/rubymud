GO_DIR := go
UI_DIR := ui
BIN_DIR := bin
BIN_NAME := mudhost
MUD ?= 127.0.0.1:4000
LISTEN ?= :8080
DB_PATH ?= data/mudhost.db

.PHONY: help run build go-build test tidy db-init db-schema ui docker-build docker-run

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
	@printf "  make docker-build                        Build Docker image\n"
	@printf "  make docker-run                          Run Docker container\n"

.PHONY: ui-install

ui-install:
	cd "$(UI_DIR)" && npm install

ui:
	cd "$(UI_DIR)" && npm run build
	$(MAKE) sync-styles

# sync-styles mirrors the theme/ANSI CSS (single source of truth in ui/src/styles)
# into the Go tree so it can be go:embed-ed for the server-side HTML log export.
# Unlike static/, this mirror is COMMITTED to git so `go build`/`go test` work
# from a clean checkout without a prior UI build; the drift guard test
# (TestEmbeddedStylesMatchSource) fails if it falls out of sync.
.PHONY: sync-styles
sync-styles:
	mkdir -p "$(GO_DIR)/internal/web/styles/ansi-themes"
	cp "$(UI_DIR)/src/styles/ansi-classes.css" "$(GO_DIR)/internal/web/styles/ansi-classes.css"
	cp "$(UI_DIR)/src/styles/export-base.css" "$(GO_DIR)/internal/web/styles/export-base.css"
	cp "$(UI_DIR)/src/styles/ansi-themes/"*.css "$(GO_DIR)/internal/web/styles/ansi-themes/"

run: build
	"./$(BIN_DIR)/$(BIN_NAME)" --mud "$(MUD)" --listen "$(LISTEN)" --db "$(DB_PATH)"

build: ui
	$(MAKE) go-build

go-build:
	mkdir -p "$(BIN_DIR)"
	cd "$(GO_DIR)" && go build -o "../$(BIN_DIR)/$(BIN_NAME)" ./cmd/mudhost

test:
	cd "$(GO_DIR)" && go test ./...

tidy:
	cd "$(GO_DIR)" && go mod tidy

db-init: go-build
	"./$(BIN_DIR)/$(BIN_NAME)" --db "$(DB_PATH)" --migrate-only

db-schema:
	sqlite3 "$(DB_PATH)" ".schema"

docker-build:
	docker build -t rubymud .

docker-run: docker-build
	docker run -it --rm -p 8080:8080 -v "$(PWD)/data:/data" rubymud
