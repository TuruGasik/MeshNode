FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/autonotif .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/autonotif /usr/local/bin/autonotif
ENTRYPOINT ["/usr/local/bin/autonotif"]
