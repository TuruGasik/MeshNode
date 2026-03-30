# =============================================================
# MeshMap – self-hosted Meshtastic node map
# Based on https://github.com/brianshea2/meshmap.net (AGPL-3.0)
# =============================================================

# ── Stage 1: Build meshobserv ────────────────────────────────
# Uses golang:latest to track the upstream meshmap.net build requirements.
# Pin to a specific version (e.g., golang:1.23) if you prefer reproducible builds.
FROM golang:latest AS build

RUN apt-get update && \
    apt-get install -y --no-install-recommends protobuf-compiler libprotobuf-dev git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

WORKDIR /build

# Clone meshmap.net source (latest to stay in sync with upstream).
# Pin to a specific commit/tag if you prefer reproducible builds.
RUN git clone --depth 1 https://github.com/brianshea2/meshmap.net.git .

# Restrict meshmap subscriptions to Indonesian channel only (msh/ID_923)
RUN perl -0pi -e 's#Topics:\s*\[\]string\{.*?\n\s*\},#Topics: []string{\n                        "msh/ID_923/2/map/",\n                        "msh/ID_923/2/e/+/+",\n                },#s' cmd/meshobserv/meshobserv.go

# Clone Meshtastic protobufs and generate Go code
RUN git clone --depth 1 https://github.com/meshtastic/protobufs.git
RUN protoc -I=protobufs -I=/usr/include \
    --go_out=internal/meshtastic --go_opt=module=github.com/meshtastic/go \
    protobufs/nanopb.proto protobufs/meshtastic/*.proto

# Build the meshobserv binary (static for alpine runtime compatibility)
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -v -o meshobserv ./cmd/meshobserv

# ── Stage 2: Runtime (nginx + meshobserv) ────────────────────
FROM nginx:alpine

# Copy meshobserv binary
COPY --from=build /build/meshobserv /usr/bin/meshobserv

# Copy website static files
COPY --from=build /build/website /usr/share/nginx/html

# Patch index.html for MeshNode Indonesia (center, bounds, branding)
COPY patch-index.sh /tmp/patch-index.sh
RUN chmod +x /tmp/patch-index.sh && /tmp/patch-index.sh && rm /tmp/patch-index.sh

# Copy custom nginx config and entrypoint
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Persistent data directory for nodes.json
RUN mkdir -p /data

EXPOSE 80

ENTRYPOINT ["/entrypoint.sh"]
