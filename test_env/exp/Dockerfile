# Use an official Python runtime as a parent image
FROM python:3.12-slim AS base

# Set the working directory in the container
WORKDIR /app
# Copy the current directory contents into the container
COPY . /app
# Install any needed packages specified in requirements.txt
RUN pip install --no-cache-dir requests

FROM infra-runner:latest

COPY --from=base / /env