FROM golang:1.21 AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/runner ./cmd/runner

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/runner /usr/local/bin/runner
ENV APP_NAME=runner
ENTRYPOINT ["/usr/local/bin/runner"]
