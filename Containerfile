FROM golang:1.24-alpine AS build

ARG PLATFORM=amd64
WORKDIR /app

RUN go env -w GOCACHE=/go-cache
RUN go env -w GOMODCACHE=/gomod-cache

RUN apk update && apk add git make upx

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN --mount=type=cache,target=/gomod-cache --mount=type=cache,target=/go-cache GOOS=linux GOARCH=${PLATFORM} make build

FROM gcr.io/distroless/static-debian12 AS runtime

ARG WORKDIR=/app
COPY --from=build /app/bin/kommodity ${WORKDIR}/kommodity

WORKDIR ${WORKDIR}

EXPOSE 8000
ENTRYPOINT ["/app/kommodity"]
