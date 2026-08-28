FROM docker.io/library/golang:1.24-alpine AS build
WORKDIR /src
COPY tools/ .
RUN CGO_ENABLED=0 go build -o /skillz .

FROM docker.io/library/alpine:3.22
# `gh skill` needs gh >= 2.90.0; the alpine package is older, so install the
# official release binary.
ARG GH_VERSION=2.90.0
RUN apk add --no-cache git \
 && arch=$(uname -m) \
 && case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac \
 && wget -qO- "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" \
    | tar -xz -C /usr/local --strip-components=1 "gh_${GH_VERSION}_linux_${arch}/bin/gh" \
 && git config --system --add safe.directory /work
COPY --from=build /skillz /usr/local/bin/skillz
WORKDIR /work
ENTRYPOINT ["skillz"]
