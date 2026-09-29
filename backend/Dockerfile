# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0

# Images of plux-server and the plux CLI (DEP-001, SEC-108). Both are
# static, cgo-free binaries running as the nonroot user on distroless
# images; the build uses the same flags as `make go-build`, so an image
# holds the same bytes as the released binary (CI-006). The server image
# also carries plux-svgc and its library, so it is based on distroless
# "base-nossl" (glibc) rather than "static".
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

# plux-svgc, the worker's SVG compiler (CMP-031, ADR-0027 Revision), and
# the Skia path operations it loads. Both are built for the target
# platform on Debian 12, the base of the server image, so they need no
# newer glibc than it has.
FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS pathops
COPY packages/plux_svgc/native/build.sh /src/build.sh
RUN sh /src/build.sh /out

FROM dart:3.13.4@sha256:33faf91bc941466a767ce845b4bbb5d578ecd180abe5ee243e9c8c039109d215 AS svgc
WORKDIR /src/packages/plux_svgc
COPY packages/plux_svgc/pubspec.yaml packages/plux_svgc/pubspec.lock ./
RUN dart pub get --enforce-lockfile
COPY packages/plux_svgc/ ./
# The golden SVG and its encoding, which the tests compare.
COPY schema/testdata/documents/widgets/assets/icons/check.svg /src/schema/testdata/documents/widgets/assets/icons/
COPY schema/testdata/bundles/widgets/variants/check.vec /src/schema/testdata/bundles/widgets/variants/
COPY --from=pathops /out/libpath_ops.so /out/libpath_ops.so
# The tests run the compiled library before the image ships it.
RUN PLUX_PATH_OPS=/out/libpath_ops.so dart test && \
    dart compile exe bin/plux_svgc.dart -o /out/plux-svgc

# Distroless "base-nossl": glibc for the Dart runtime and the library;
# still no shell and no package manager (SEC-108).
FROM gcr.io/distroless/base-nossl-debian12:nonroot@sha256:be40c00dfabd86576d92666e87e406714d5618342de1a0c213ad232de255172e AS server
COPY --from=build /out/plux-server /usr/local/bin/plux-server
COPY --from=svgc /out/plux-svgc /usr/local/bin/plux-svgc
COPY --from=svgc /out/libpath_ops.so /usr/local/lib/plux/libpath_ops.so
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
