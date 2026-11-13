# syntax=docker/dockerfile:1
ARG GO_VERSION=1.22
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder
WORKDIR /src
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/root/.cache go build -o /out/app ./...
FROM alpine:3.19
COPY --from=builder --chown=app:app /out/app /usr/local/bin/app
EXPOSE 8080/tcp 9090
USER app
ENTRYPOINT ["/usr/local/bin/app"]
CMD ["--port", "8080"]
