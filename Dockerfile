# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

ARG GO_IMAGE=golang:1.26.6-alpine3.23@sha256:e57c41c1d5864341031181b0db34b9a537bb5773eb6428e4e5bdaea0f9135406
FROM ${GO_IMAGE} AS build

WORKDIR /src
COPY go.mod go.sum ./
# Keep the complete pinned module graph in the build layer. The test-runtime
# stage runs packages that are intentionally absent from the release binary,
# so an ephemeral module cache would make the hermetic test gate fetch at run
# time and fail when Docker DNS or outbound access is unavailable.
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=development
ARG REVISION=unknown
ARG CREATED=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
    -ldflags="-s -w -buildid= -X github.com/tinfoyle/spyglass-engine/internal/platform/buildinfo.Version=${VERSION} -X github.com/tinfoyle/spyglass-engine/internal/platform/buildinfo.Revision=${REVISION} -X github.com/tinfoyle/spyglass-engine/internal/platform/buildinfo.BuiltAt=${CREATED}" \
    -o /out/spyglass ./cmd/spyglass
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
    -o /out/prototype-transform ./cmd/prototype-transform
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
    -o /out/prototype-import ./cmd/prototype-import

FROM build AS test-runtime

RUN apk add --no-cache gcc musl-dev
COPY Dockerfile .dockerignore ./
COPY .github/workflows/release-image.yml ./.github/workflows/release-image.yml
COPY .github/workflows/release-website-image.yml ./.github/workflows/release-website-image.yml
COPY .github/workflows/verify.yml ./.github/workflows/verify.yml
COPY api ./api
COPY deploy/package-surface-inventory.json ./deploy/package-surface-inventory.json
COPY website/lib/generated ./website/lib/generated

FROM scratch

ARG VERSION=development
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Infinite Ocean: Spyglass" \
      org.opencontainers.image.description="Shared multi-mode Spyglass application and worker runtime" \
      org.opencontainers.image.source="https://github.com/tinfoyle/mainspring-engine" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="Proprietary"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/spyglass /spyglass
COPY --from=build --chown=65532:65532 /out/prototype-transform /prototype-transform
COPY --from=build --chown=65532:65532 /out/prototype-import /prototype-import

USER 65532:65532
EXPOSE 8080 8081 8443
ENTRYPOINT ["/spyglass"]
