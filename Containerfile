FROM golang:1.26-trixie AS build
ARG VERSION

WORKDIR /app
COPY go.* ./
RUN go mod download

ADD Makefile .
ADD cmd/ cmd/
RUN VERSION=$VERSION BINARY=app make build

FROM gcr.io/distroless/static:nonroot AS run
WORKDIR /
COPY --from=build /app/app .
USER 65532:65532
ENTRYPOINT [ "/app" ]
