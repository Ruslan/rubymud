FROM node:24-alpine AS builder

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
RUN CGO_ENABLED=0 make build

FROM alpine:latest

ENV MUD_HOST=""
ENV UI_PORT=8080
ENV DATA_DIR="/data"

# Ensure data dir exists
RUN mkdir -p /data

COPY --from=builder /build/bin/mudhost /mudhost

EXPOSE ${UI_PORT}
VOLUME ${DATA_DIR}

# Using exec to ensure the Go app receives OS signals (like SIGTERM) properly
CMD ["/bin/sh", "-c", "exec /mudhost --mud \"${MUD_HOST}\" --listen \":${UI_PORT}\" --db \"${DATA_DIR}/mudhost.db\""]
