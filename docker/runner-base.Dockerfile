FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/runner ./cmd/runner

FROM alpine:3.19
RUN apk add --no-cache bash coreutils busybox-extras ca-certificates \
    && mkdir -p /env/tmp \
    && chmod 755 /env
WORKDIR /app
ENV APP_NAME=runner \
    RUNNER_CHROOT_PATH=/env \
    RUNNER_WORKSPACE_ROOT=/env/tmp
COPY --from=builder /out/runner /usr/local/bin/runner
ENTRYPOINT ["/usr/local/bin/runner"]
