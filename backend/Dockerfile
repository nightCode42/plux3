# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0

# Images of plux-server and the plux CLI (DEP-001, SEC-108). Both are
# static, cgo-free binaries on distroless "static" running as the nonroot
# user; the build uses the same flags as `make go-build`, so an image
# holds the same bytes as the released binary (CI-006).
#
#   docker buildx build -f backend/Dockerfile --target server .
#   docker buildx build -f backend/Dockerfile --target cli .
#
# Base images are pinned by digest and updated by Dependabot.

FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG COMMIT_DATE=unknown
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend/ ./backend/
RUN cd backend && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
      -ldflags "-s -w -buildid= \
        -X github.com/nightCode42/plux3/backend/internal/buildinfo.version=$VERSION \
        -X github.com/nightCode42/plux3/backend/internal/buildinfo.commit=$COMMIT \
        -X github.com/nightCode42/plux3/backend/internal/buildinfo.commitDate=$COMMIT_DATE" \
      -o /out/ ./cmd/plux-server ./cmd/plux && mkdir -p /out/keys

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS server
COPY --from=build /out/plux-server /usr/local/bin/plux-server
# The file signing backend's directory, owned by nonroot so a volume
# mounted there is writable with a read-only root filesystem.
COPY --from=build --chown=65532:65532 /out/keys /var/lib/plux/keys
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/plux-server"]
CMD ["serve", "--config", "/etc/plux/plux.yaml"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS cli
COPY --from=build /out/plux /usr/local/bin/plux
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/plux"]
