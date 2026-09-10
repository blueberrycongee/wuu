FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/wuu-server ./cmd/wuu-server
COPY internal/remote ./internal/remote
COPY internal/statepath ./internal/statepath
RUN CGO_ENABLED=0 go build -trimpath -o /wuu-server ./cmd/wuu-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home wuu && mkdir /data && chown wuu:wuu /data
COPY --from=build /wuu-server /usr/local/bin/wuu-server
USER wuu
VOLUME /data
EXPOSE 8787
ENTRYPOINT ["wuu-server", "--addr", "0.0.0.0:8787", "--state", "/data/relay.json", "--accounts", "/data/accounts.db"]
