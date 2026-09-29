FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /wuu ./cmd/wuu

FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git openssh-client python3 ripgrep tar unzip \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /workspace
COPY --from=build /wuu /usr/local/bin/wuu
WORKDIR /workspace
CMD ["sleep", "infinity"]
