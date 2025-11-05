#!/bin/bash
docker run --rm -v "$PWD":/src -w /src golang:1.21 gofmt -w ./internal ./cmd
docker run --rm -v "$PWD":/src -w /src golang:1.21 go mod tidy
docker run --rm -v "$PWD":/src -w /src golang:1.21 go test ./...
docker compose up -d --build
