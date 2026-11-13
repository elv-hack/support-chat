FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY open-im-server/go.mod open-im-server/go.sum ./
RUN go mod download
COPY open-im-server/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /openim ./cmd

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates python3 python3-yaml && rm -rf /var/lib/apt/lists/* && useradd -u 10002 -m app && mkdir /runtime && chown app:app /runtime
COPY --from=build /openim /usr/local/bin/openim
COPY open-im-server/config/ /defaults/
COPY support-chat/scripts/configure_openim.py /configure.py
USER app
CMD ["sh","-c","python3 /configure.py --source /defaults --dest /runtime/config --docker && exec openim -c /runtime/config"]
