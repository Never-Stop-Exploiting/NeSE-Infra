FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/runner ./cmd/runner

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/runner /usr/local/bin/runner
ENV APP_NAME=runner \
    RUN_QUEUE_RETRY_DELAY=0s
ENTRYPOINT ["/usr/local/bin/runner"]
