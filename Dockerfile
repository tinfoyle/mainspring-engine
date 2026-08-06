# syntax=docker/dockerfile:1.7
FROM golang:1.26.3-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mainspring ./cmd/mainspring

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mainspring /usr/local/bin/mainspring
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mainspring"]
