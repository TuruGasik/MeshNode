# =============================================================
# MeshMap – self-hosted Meshtastic node map
# Based on https://github.com/brianshea2/meshmap.net (AGPL-3.0)
# Customized by https://github.com/TuruGasik/MeshNode
# =============================================================

# ── Stage 1: Build meshobserv ────────────────────────────────
FROM golang:1.25-bookworm AS build

RUN apt-get update && \
    apt-get install -y --no-install-recommends protobuf-compiler libprotobuf-dev git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

WORKDIR /build

# Copy local source code (already patched)
COPY go.mod .
COPY cmd/ cmd/
COPY internal/ internal/

# Clone Meshtastic protobufs and generate Go code
RUN git clone --depth 1 https://github.com/meshtastic/protobufs.git
RUN protoc -I=protobufs -I=/usr/include \
    --go_out=internal/meshtastic --go_opt=module=github.com/meshtastic/go \
    protobufs/nanopb.proto protobufs/meshtastic/*.proto

# Fetch dependencies and build
RUN GONOSUMCHECK='meshnode.id/*' GONOSUMDB='meshnode.id/*' GOFLAGS=-mod=mod \
    go get modernc.org/sqlite && go mod tidy && CGO_ENABLED=0 GOOS=linux go build -v -o meshobserv ./cmd/meshobserv

# ── Stage 2: Runtime ─────────────────────────────────────────
FROM nginx:alpine

# Copy meshobserv binary
COPY --from=build /build/meshobserv /usr/bin/meshobserv

# Copy website static files (with Indonesia customizations)
COPY ./website /usr/share/nginx/html

# Copy custom nginx config and entrypoint
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Persistent data directory for nodes.json
RUN mkdir -p /data

EXPOSE 80

ENTRYPOINT ["/entrypoint.sh"]