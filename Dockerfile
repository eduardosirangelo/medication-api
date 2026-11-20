# syntax=docker/dockerfile:1

# --- build -------------------------------------------------------------------
# Full image, not alpine: it ships git, which `go build` needs for the VCS stamp.
FROM golang:1.27.4 AS build
WORKDIR /src
# The checkout is owned by the host user; without this git refuses to read it
# and the build loses its commit stamp.
RUN git config --global --add safe.directory /src

# Dependencies first, so editing code does not invalidate this layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api

# --- runtime -----------------------------------------------------------------
# Static binary on distroless: no shell, no package manager, CA certificates
# and tzdata included; the nonroot tag runs as UID 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
CMD ["serve"]
