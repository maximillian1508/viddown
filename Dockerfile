# syntax=docker/dockerfile:1

FROM node:22-bookworm AS web
WORKDIR /web
COPY web/package.json ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.23-bookworm AS api
WORKDIR /src
# Prefer IPv4 — some hosts fail reaching proxy.golang.org over IPv6
RUN printf 'precedence :ffff:0:0/96  100\n' >> /etc/gai.conf
ENV GOPROXY=https://proxy.golang.org,direct
COPY api/ ./
COPY --from=web /web/dist ./web
RUN go mod tidy && CGO_ENABLED=0 go build -o /out/viddown .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    ffmpeg \
    ca-certificates \
    fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=api /out/viddown /app/viddown

ENV LISTEN_ADDR=:8091 \
    OUTPUT_DIR=/data/output \
    PROBE_TIMEOUT=45s \
    CHROME_PATH=/usr/bin/chromium \
    HOME=/home/viddown \
    XDG_CONFIG_HOME=/home/viddown/.config \
    XDG_CACHE_HOME=/home/viddown/.cache \
    TZ=Asia/Kuala_Lumpur

RUN mkdir -p /data/output \
      /home/viddown/.config \
      /home/viddown/.cache \
      /tmp/chromium-crash \
      /tmp/chromium-user \
    && chown -R 1000:1000 /data /app /home/viddown /tmp/chromium-crash /tmp/chromium-user
USER 1000:1000
EXPOSE 8091
CMD ["/app/viddown"]
