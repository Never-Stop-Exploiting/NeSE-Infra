FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/dispatcher ./cmd/dispatcher

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/dispatcher /usr/local/bin/dispatcher
ENV APP_NAME=dispatcher
ENTRYPOINT ["/usr/local/bin/dispatcher"]
