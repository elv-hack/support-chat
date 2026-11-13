FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY support-chat/web/package*.json ./
RUN npm ci
COPY support-chat/web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS server
WORKDIR /src/server
COPY support-chat/server/go.mod support-chat/server/go.sum ./
RUN go mod download
COPY support-chat/server/ ./
ARG APP_VERSION=dev
RUN CGO_ENABLED=1 go build -mod=readonly -trimpath -ldflags "-X main.buildVersion=${APP_VERSION}" -o /gateway .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/* && useradd -u 10001 -m app && mkdir /data && chown app:app /data
COPY --from=server /gateway /usr/local/bin/gateway
COPY --from=web /src/web/dist /app/web
USER app
ENV LISTEN=0.0.0.0:8090 DATA_DIR=/data WEB_DIR=/app/web
EXPOSE 8090
CMD ["gateway"]
