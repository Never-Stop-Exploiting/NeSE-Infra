# Use an official Python runtime as a parent image
FROM python:3.12-slim AS base

# Set the working directory in the container
WORKDIR /app
# Copy the current directory contents into the container
COPY . /app
# Install any needed packages specified in requirements.txt
RUN pip install --no-cache-dir requests

# FROM infra-runner:latest

# COPY --from=base / /env
FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/runner ./cmd/runner

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/runner /usr/local/bin/runner
COPY --from=base / /env
ENV APP_NAME=runner
ENTRYPOINT ["/usr/local/bin/runner"]
