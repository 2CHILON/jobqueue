# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26

# ---- Stage 1: compile ----
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/jobqueue-server ./cmd/server

# ---- Stage 2: tiny runtime image ----
FROM alpine:3.20
RUN addgroup -S app && adduser -S -G app app \
    && mkdir /data && chown app:app /data
USER app
ENV PORT=8080 \
    DB_PATH=/data/jobqueue.db
EXPOSE 8080
COPY --from=build /out/jobqueue-server /usr/local/bin/jobqueue-server
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["jobqueue-server"]