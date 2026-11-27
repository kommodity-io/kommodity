FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build-api

ARG TARGETARCH
ARG TARGETOS
ARG VERSION
ARG TREE_STATE
ARG COMMIT
ARG COMPRESS=false

# Pin toolchain so a go.mod toolchain bump doesn't invalidate the build cache.
ENV GOTOOLCHAIN=local

WORKDIR /app

RUN go env -w GOCACHE=/go-cache
RUN go env -w GOMODCACHE=/gomod-cache

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/gomod-cache go mod download

# Copy only Go sources and embedded assets so non-Go changes (charts,
# docs, terraform) don't invalidate the compile cache.
COPY public ./public
COPY cmd ./cmd
COPY pkg ./pkg
RUN --mount=type=cache,target=/gomod-cache --mount=type=cache,target=/go-cache \
    go build -ldflags "-X 'k8s.io/component-base/version.gitVersion=${VERSION}' -X 'k8s.io/component-base/version.gitTreeState=${TREE_STATE}' -X 'k8s.io/component-base/version.gitCommit=${COMMIT}'" \
    -o bin/kommodity ./cmd/kommodity

# Compress the binary for published images (COMPRESS=true). Local builds keep
# it uncompressed: faster container start and the binary stays debuggable.
RUN if [ "${COMPRESS}" = "true" ]; then \
        apk add --no-cache upx && upx -qq bin/kommodity; \
    fi

FROM gcr.io/distroless/static-debian12 AS runtime

ARG WORKDIR=/app
COPY --from=build-api /app/bin/kommodity ${WORKDIR}/kommodity
COPY --from=build-api /app/public ${WORKDIR}/public

WORKDIR ${WORKDIR}

EXPOSE 5000
ENTRYPOINT ["/app/kommodity"]
