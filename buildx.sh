#!/bin/bash
docker buildx build -t ghcr.io/never-stop-exploiting/runner:latest -f ./docker/runner.Dockerfile . --push
docker buildx build -t ghcr.io/never-stop-exploiting/submitter:latest -f ./docker/flag-submitter.Dockerfile . --push