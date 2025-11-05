FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/control-plane ./cmd/control-plane

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/control-plane /usr/local/bin/control-plane
ENV APP_NAME=control-plane
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/control-plane"]
