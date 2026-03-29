# =============================================================
# MeshMap – self-hosted Meshtastic node map
# Based on https://github.com/brianshea2/meshmap.net (AGPL-3.0)
# =============================================================

# ── Stage 1: Build meshobserv ────────────────────────────────
FROM golang:latest AS build

RUN apt-get update && \
    apt-get install -y --no-install-recommends protobuf-compiler git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

WORKDIR /build

# Clone meshmap.net source
RUN git clone --depth 1 https://github.com/brianshea2/meshmap.net.git .

# Clone Meshtastic protobufs and generate Go code
RUN git clone --depth 1 https://github.com/meshtastic/protobufs.git
RUN protoc -I=protobufs \
    --go_out=internal/meshtastic --go_opt=module=github.com/meshtastic/go \
    protobufs/nanopb.proto protobufs/meshtastic/*.proto

# Build the meshobserv binary
RUN go mod tidy && go build -v -o meshobserv ./cmd/meshobserv

# ── Stage 2: Runtime (nginx + meshobserv) ────────────────────
FROM nginx:alpine

# Copy meshobserv binary
COPY --from=build /build/meshobserv /usr/bin/meshobserv

# Copy website static files
COPY --from=build /build/website /usr/share/nginx/html

# Copy custom nginx config and entrypoint
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Persistent data directory for nodes.json
RUN mkdir -p /data

EXPOSE 80

ENTRYPOINT ["/entrypoint.sh"]
