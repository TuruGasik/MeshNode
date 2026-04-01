# Stage 1: Build the Go binary
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod ./
COPY *.go ./
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mqtt-relay .

# Stage 2: Minimal runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/mqtt-relay /usr/local/bin/mqtt-relay
CMD ["mqtt-relay"]
