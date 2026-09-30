# syntax=docker/dockerfile:1
FROM golang:1.27-trixie AS build
ARG VERSION

WORKDIR /app
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

ADD Makefile .
ADD cmd/ cmd/
ADD pkg/ pkg/
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	VERSION=$VERSION BINARY=app make build

FROM gcr.io/distroless/static:nonroot AS run
WORKDIR /
COPY --from=build /app/app .
USER 65532:65532
ENTRYPOINT [ "/app" ]
