FROM node:18 AS builder

WORKDIR /web
COPY ./VERSION .
COPY ./web .

RUN npm install --prefix /web/default & \
    npm install --prefix /web/berry & \
    npm install --prefix /web/air & \
    wait

RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) npm run build --prefix /web/default & \
    DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) npm run build --prefix /web/berry & \
    DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) npm run build --prefix /web/air & \
    wait

FROM golang:1.24.0-bullseye AS builder2

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    sqlite3 libsqlite3-dev && \
    rm -rf /var/lib/apt/lists/*

# Declare TARGETARCH so BuildKit can set it correctly.
ARG TARGETARCH=amd64
ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux \
    CGO_CFLAGS="-I/usr/include" \
    CGO_LDFLAGS="-L/usr/lib" \
    GOARCH=$TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /web/build ./web/build

# For ARM64 builds, install and use the appropriate cross-compiler.
RUN if [ "$GOARCH" = "arm64" ]; then \
        apt-get update && apt-get install -y gcc-aarch64-linux-gnu; \
    fi

# Use a single RUN command to conditionally set CC for ARM64.
RUN if [ "$GOARCH" = "arm64" ]; then \
        CC=aarch64-linux-gnu-gcc go build -trimpath -ldflags "-s -w -X github.com/songquanpeng/one-api/common.Version=$(cat VERSION)" -o one-api; \
    else \
        go build -trimpath -ldflags "-s -w -X github.com/songquanpeng/one-api/common.Version=$(cat VERSION)" -o one-api; \
    fi

FROM debian:bullseye

RUN rm /var/lib/dpkg/info/libc-bin.*

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata bash haveged libc-bin ffmpeg && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder2 /build/one-api /

EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]
