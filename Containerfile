FROM node:22-slim AS build-ui

WORKDIR /app
COPY Makefile ./
COPY ./pkg/ui/web/kommodity-ui ./pkg/ui/web/kommodity-ui

RUN apt-get update && apt-get install -y make

RUN make build-ui

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build-api

# This is set automatically by buildx
ARG TARGETARCH
ARG TARGETOS

WORKDIR /app

RUN go env -w GOCACHE=/go-cache
RUN go env -w GOMODCACHE=/gomod-cache

RUN apk update && apk add git make upx

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=build-ui /app/pkg/ui/web/kommodity-ui/dist ./pkg/ui/web/kommodity-ui/dist
RUN --mount=type=cache,target=/gomod-cache --mount=type=cache,target=/go-cache GOOS=${TARGETOS} GOARCH=${TARGETARCH} make build-api

FROM gcr.io/distroless/static-debian12 AS runtime

ARG WORKDIR=/app
COPY --from=build-api /app/bin/kommodity ${WORKDIR}/kommodity
COPY --from=build-ui /app/pkg/ui/web/kommodity-ui/dist ${WORKDIR}/pkg/ui/web/kommodity-ui/dist

WORKDIR ${WORKDIR}

EXPOSE 5000
ENTRYPOINT ["/app/kommodity"]
