#!/bin/bash
docker run --rm -v "$PWD":/src -w /src golang:1.25 gofmt -w ./internal ./cmd
docker run --rm -v "$PWD":/src -w /src golang:1.25 go mod tidy
docker run --rm -v "$PWD":/src -w /src golang:1.25 go test ./...
docker compose up -d --build
