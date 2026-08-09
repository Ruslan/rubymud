FROM node:24-alpine AS builder

ARG VERSION=dev

RUN apk add --no-cache go make

WORKDIR /build

COPY Makefile ./

# Cache Node.js dependencies
COPY ui/package.json ui/package-lock.json* ./ui/
RUN make ui-install

# Cache Go module dependencies
COPY go/go.mod go/go.sum ./go/
RUN cd go && go mod download

# Copy source code and build
COPY ui ./ui
COPY go ./go
RUN CGO_ENABLED=0 make build VERSION="${VERSION}"

FROM alpine:latest

ENV MUD_HOST=""
ENV UI_PORT=8080
# Docker follows the local-first default. Set UI_LISTEN=0.0.0.0:8080 only with
# paired MUDHOST_BASIC_AUTH_USER/PASS or a database which already enables Web Access.
ENV UI_LISTEN=""
ENV DATA_DIR="/data"

# Ensure data dir exists
RUN mkdir -p /data

COPY --from=builder /build/bin/mudhost /mudhost

EXPOSE ${UI_PORT}
VOLUME ${DATA_DIR}

# Using exec to ensure the Go app receives OS signals (like SIGTERM) properly
CMD ["/bin/sh", "-c", "if [ -n \"${UI_LISTEN}\" ]; then set -- --listen \"${UI_LISTEN}\"; elif [ \"${UI_PORT}\" != 8080 ]; then set -- --listen \"127.0.0.1:${UI_PORT}\"; else set --; fi; exec /mudhost --mud \"${MUD_HOST}\" \"$@\" --db \"${DATA_DIR}/mudhost.db\""]
