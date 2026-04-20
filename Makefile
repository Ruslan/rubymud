GO_DIR := go
MUD ?= 127.0.0.1:4000
LISTEN ?= :8080

.PHONY: help run build tidy

help:
	@printf "Targets:\n"
	@printf "  make run MUD=host:port [LISTEN=:8080]   Build and run mudhost\n"
	@printf "  make build                               Build mudhost binary\n"
	@printf "  make tidy                                Run go mod tidy\n"

run:
	cd "$(GO_DIR)" && go run ./cmd/mudhost --mud "$(MUD)" --listen "$(LISTEN)"

build:
	cd "$(GO_DIR)" && go build ./cmd/mudhost

tidy:
	cd "$(GO_DIR)" && go mod tidy
