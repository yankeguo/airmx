FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Short commit SHA, injected into static asset URLs for cache busting
# (.git is dockerignored, so it must come in as a build arg).
ARG REVISION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.assetVersion=${REVISION}" -o /airmx .

# The image ships no default config file and no data directory: mount your
# own config at /etc/airmx/config.yaml and set data_dir to a mounted volume.
# ca-certificates is required for Web Push (HTTPS to the vendor push
# services); Alpine also provides a shell for debugging.
FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /airmx /usr/local/bin/airmx
EXPOSE 25 8080
ENTRYPOINT ["/usr/local/bin/airmx", "-config", "/etc/airmx/config.yaml"]
