# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM golang:1.26.6-alpine3.23@sha256:e57c41c1d5864341031181b0db34b9a537bb5773eb6428e4e5bdaea0f9135406 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o /out/google-oauth-fixture ./cmd/google-oauth-fixture
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o /out/provider-secret-init ./cmd/provider-secret-init

FROM scratch AS fixture
COPY --from=build --chown=65532:65532 /out/google-oauth-fixture /google-oauth-fixture
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/google-oauth-fixture"]

FROM scratch AS secret-init
COPY --from=build /out/provider-secret-init /provider-secret-init
ENTRYPOINT ["/provider-secret-init"]
