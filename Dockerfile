# The builder runs on the build platform and cross-compiles, so multi-arch builds need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.11-trixie@sha256:fa340cb59ebeb1d53ac1cccfc334201d938fe5f3458003cccb767096d8e032b8 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /workspace
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT}" -o manager ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
ARG VERSION=dev
ARG COMMIT=unknown
# RFC 3339 form of SOURCE_DATE_EPOCH, so a rebuild of the same commit yields the same image digest.
ARG CREATED=1970-01-01T00:00:00Z
LABEL org.opencontainers.image.title="openbao-config-operator" \
      org.opencontainers.image.description="Manage the configuration of an existing OpenBao server through Kubernetes custom resources." \
      org.opencontainers.image.source="https://github.com/alexandrust88/openbao-config-operator" \
      org.opencontainers.image.url="https://github.com/alexandrust88/openbao-config-operator" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${CREATED}"
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
