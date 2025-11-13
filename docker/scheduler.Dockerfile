FROM golang:1.25 AS builder
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/scheduler ./cmd/scheduler

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/scheduler /usr/local/bin/scheduler
ENV APP_NAME=scheduler
ENTRYPOINT ["/usr/local/bin/scheduler"]
