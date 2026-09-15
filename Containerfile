FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build-api

# This is set automatically by buildx
ARG TARGETARCH
ARG TARGETOS
ARG VERSION

WORKDIR /app

RUN go env -w GOCACHE=/go-cache
RUN go env -w GOMODCACHE=/gomod-cache

RUN apk update && apk add git make upx

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN --mount=type=cache,target=/gomod-cache --mount=type=cache,target=/go-cache GOOS=${TARGETOS} GOARCH=${TARGETARCH} VERSION=${VERSION} make build-api

FROM gcr.io/distroless/static-debian12 AS runtime

ARG WORKDIR=/app
COPY --from=build-api /app/bin/kommodity ${WORKDIR}/kommodity
COPY --from=build-api /app/public ${WORKDIR}/public

WORKDIR ${WORKDIR}

EXPOSE 5000
ENTRYPOINT ["/app/kommodity"]
