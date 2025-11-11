FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/control-panel ./cmd/control-panel

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/control-panel /usr/local/bin/control-panel
ENV APP_NAME=control-panel
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/control-panel"]
