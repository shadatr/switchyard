# syntax=docker/dockerfile:1
# Multi-stage build: static Go binaries on a distroless, non-root base.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/switchyard ./cmd/switchyard && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/mockllm ./cmd/mockllm

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/switchyard /out/mockllm /
COPY deploy/switchyard.docker.yaml /etc/switchyard/switchyard.yaml
USER nonroot:nonroot
EXPOSE 8787
ENTRYPOINT ["/switchyard"]
CMD ["-config", "/etc/switchyard/switchyard.yaml"]
