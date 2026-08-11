FROM alpine:latest AS certs
RUN apk add --no-cache ca-certificates

FROM golang:1.25-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/webtor-io/webtor-cli/cmd.Version=${VERSION}" -o /webtor .

FROM alpine:latest
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /webtor /usr/local/bin/webtor
ENTRYPOINT ["webtor"]
