FROM node:24-alpine AS builder

RUN apk add --no-cache go make

WORKDIR /build

COPY go ./go
COPY ui ./ui
COPY Makefile ./

RUN make ui-install
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
