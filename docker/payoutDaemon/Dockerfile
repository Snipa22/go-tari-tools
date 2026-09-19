# syntax=docker/dockerfile:1
#
# docker/payoutDaemon/Dockerfile
#
# Builds cmd/payoutDaemon, the Tari payout batching daemon.
#
# Runtime base: gcr.io/distroless/static-debian12. payoutDaemon never
# shells out (no os/exec usage in this package tree) but does make
# outbound calls to Postgres (pgx), Redis, and the Tari wallet GRPC
# service, so it needs CA certificates for TLS verification on those
# connections. distroless/static ships ca-certificates and a built-in
# nonroot (65532:65532) user, which covers both needs without pulling
# in a shell or package manager.

FROM golang:1.24-bookworm AS builder

WORKDIR /src

# Cache go.mod/go.sum download separately from source for better
# layer caching across builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/payoutDaemon \
    ./cmd/payoutDaemon

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder /out/payoutDaemon /usr/local/bin/payoutDaemon

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/payoutDaemon"]
