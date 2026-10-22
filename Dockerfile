# tidebot server image: agentd + agentctl. The claude CLI is not bundled; add it in your own
# image (see docs/release.md), e.g. FROM ghcr.io/zhengmingming1/tidebot + npm i -g @anthropic-ai/claude-code
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/Zhengmingming1/tidebot/internal/buildinfo.Version=${VERSION} -X github.com/Zhengmingming1/tidebot/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/agentd ./cmd/agentd && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /out/agentctl ./cmd/agentctl

FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git tzdata && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 10001 tidebot
COPY --from=build /out/agentd /out/agentctl /usr/local/bin/
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/tidebot/
USER tidebot
ENV TIDEBOT_HOME=/home/tidebot/.tidebot \
    TIDEBOT_WEBUI=1 \
    TIDEBOT_WEBUI_ADDR=0.0.0.0:7420 \
    TIDEBOT_WEBUI_ALLOW_LAN=1 \
    TIDEBOT_WEBUI_ALLOW_HTTP=1 \
    TIDEBOT_MANAGED=1 \
    TIDEBOT_LOG_FORMAT=json
VOLUME ["/home/tidebot/.tidebot"]
EXPOSE 7420
ENTRYPOINT ["agentd"]
