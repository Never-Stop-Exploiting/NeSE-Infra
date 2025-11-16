FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/flag-submitter ./cmd/flag-submitter

FROM alpine:3.19
LABEL org.opencontainers.image.source=https://github.com/Never-Stop-Exploiting/NeSE-Infra
RUN apk add --no-cache bash coreutils busybox-extras ca-certificates \
    && mkdir -p /env/tmp/submitter \
    && chmod 755 /env
WORKDIR /app
ENV APP_NAME=flag-submitter \
    SUBMITTER_CHROOT_PATH=/env \
    SUBMITTER_PACKAGE_PATH=/tmp/submitter
COPY --from=builder /out/flag-submitter /usr/local/bin/flag-submitter
ENTRYPOINT ["/usr/local/bin/flag-submitter"]
