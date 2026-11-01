# syntax=docker/dockerfile:1

# BUILDPLATFORM keeps the toolchain native while cross-compiling, so building
# for a Raspberry Pi from an amd64 host does not run Go under emulation.
#
# Pin a patched Go release, not just the minor line: the go directive in go.mod
# is only a minimum, and the standard library in an early 1.25 patch carries
# advisories that would otherwise end up in the shipped binary. Verified with
# govulncheck under this exact toolchain.
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine3.24 AS build

WORKDIR /app

# Dependencies first so the layer is reused whenever only sources change.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# TARGETOS/TARGETARCH are provided by buildx; the defaults keep a plain
# "docker build" working. VERSION is injected instead of being derived from git,
# which lets .dockerignore keep the .git directory out of the build context.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
    -trimpath \
    -ldflags="-w -s -X main.Version=${VERSION}" \
    -o /out/cheap-switch-exporter \
    .

# Nothing in the image but the static binary. It runs as any uid it is given.
FROM scratch

LABEL maintainer="Martin Kjellstrand <madworx@github>"
LABEL org.opencontainers.image.source="https://github.com/madworx/cheap-switch-exporter"
LABEL org.opencontainers.image.description="Prometheus Exporter for cheap switch boxes without SNMP"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /out/cheap-switch-exporter /bin/cheap-switch-exporter
COPY config.yaml.example /etc/cheap-switch-exporter/config.yaml.example

USER 1000:1000

VOLUME ["/etc/cheap-switch-exporter"]
EXPOSE 8080

# Liveness only: /healthz does not poll the switch, so the health check cannot
# turn into a second scraper hammering the device.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
    CMD ["/bin/cheap-switch-exporter", "-healthcheck"]

ENTRYPOINT ["/bin/cheap-switch-exporter"]
CMD ["-c", "/etc/cheap-switch-exporter/config.yaml"]
