FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /airmx .

# The image ships no default config file and no data directory: mount your
# own config at /etc/airmx/config.yaml and set data_dir to a mounted volume.
FROM scratch
COPY --from=build /airmx /airmx
EXPOSE 25 8080
ENTRYPOINT ["/airmx", "-config", "/etc/airmx/config.yaml"]
