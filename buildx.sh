#!/bin/bash
docker buildx build -t ghcr.io/never-stop-exploiting/runner:dev -f ./docker/runner.Dockerfile . --push
docker buildx build -t ghcr.io/never-stop-exploiting/submitter:dev -f ./docker/flag-submitter.Dockerfile . --push