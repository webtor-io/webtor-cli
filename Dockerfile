# Multi-arch: the build stage always runs on the build host's own
# architecture and Go cross-compiles for the target — no QEMU-emulated
# compilation. Certs are architecture-independent, so that stage is pinned
# to the build platform too.
FROM --platform=$BUILDPLATFORM alpine:latest AS certs
RUN apk add --no-cache ca-certificates

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-s -w -X github.com/webtor-io/webtor-cli/cmd.Version=${VERSION}" -o /webtor .

FROM alpine:latest
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /webtor /usr/local/bin/webtor
ENTRYPOINT ["webtor"]
