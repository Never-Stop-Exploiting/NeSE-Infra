FROM golang:1.21 AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/flag-submitter ./cmd/flag-submitter

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/flag-submitter /usr/local/bin/flag-submitter
ENV APP_NAME=flag-submitter
ENTRYPOINT ["/usr/local/bin/flag-submitter"]
