FROM mcr.microsoft.com/devcontainers/base:jammy

# Install Earthbuild
RUN curl --silent --location https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-linux-amd64 --output /usr/local/bin/earthbuild && \
  chmod +x /usr/local/bin/earthbuild && \
  /usr/local/bin/earthbuild bootstrap --with-autocomplete
