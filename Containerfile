FROM golang:1.20 AS build
ARG VERSION
ARG TARGET

RUN apt-get update && apt-get install -y upx-ucl

WORKDIR /app
COPY go.* ./
RUN go mod download

ADD Makefile .
ADD cmd/ cmd/
RUN TARGET=$TARGET VERSION=$VERSION UPX=-9 BINARY=app make build

FROM gcr.io/distroless/static:nonroot AS run
WORKDIR /
COPY --from=build /app/app .
USER 65532:65532
ENTRYPOINT [ "/app" ]
