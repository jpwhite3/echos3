# Start from the official Go Dev Container image, which includes Go and other common tools.
# See here for more image variants: https://mcr.microsoft.com/en-us/product/devcontainers/go/tags
FROM mcr.microsoft.com/devcontainers/go:1.24

# The USER_UID and USER_GID variables will be passed in by the Dev Container extension
# to match your local user, which prevents file permission issues.
ARG USER_UID=1000
ARG USER_GID=$USER_UID

# Install the linter tool used by the project's Makefile
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# [Optional] You could add any other OS-level packages here using apt-get.
# For example:
# RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
#     && apt-get -y install --no-install-recommends <your-package-here>
