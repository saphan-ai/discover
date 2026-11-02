# Discover's image: the static discover binary and git, on Alpine, run as a user that is not root.
#
# The defaults below are the values in versions.env; make passes those settings and they win. Both are pinned by
# version and digest, and scripts/image-pins.sh refuses the build when either is not.
# git is in the image because discover reads a git repository's branch, HEAD and state through git.

ARG GOLANG_IMAGE=golang:1.26.10@sha256:670bb5aa5349ae03b8516764718486f4f16399134bdc9acd0c46a23f043ab05c
ARG RUNTIME_IMAGE=alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

# The binary is cross-compiled on the build machine's own platform, so a multi-platform build needs no emulation.
FROM --platform=$BUILDPLATFORM ${GOLANG_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/saphan-ai/discover/internal/cli.version=${VERSION} -X github.com/saphan-ai/discover/internal/cli.commit=${COMMIT}" \
      -o /out/discover ./cmd/discover

FROM ${RUNTIME_IMAGE}
ARG CREATED=unset
ARG REVISION=unset
ARG VERSION=unset
LABEL org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="discover" \
      org.opencontainers.image.description="Reads a repository without changing it and proves it changed nothing." \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/saphan-ai/discover"
RUN apk add --no-cache git \
 && adduser -D -u 65532 -h /work discover
COPY --from=build /out/discover /usr/local/bin/discover
USER 65532:65532
WORKDIR /work
ENTRYPOINT ["discover"]
CMD ["help"]
