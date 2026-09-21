# Refresh digests with: docker buildx imagetools inspect golang:1.23 --format '{{json .Manifest.Digest}}'
FROM golang:1.23@sha256:73f0632491fa6e927dd514f7ce9556d6a524aeb9d3b036c84f8ee732e4d0c117 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOFLAGS=-mod=readonly go build -trimpath -buildvcs=true \
    -ldflags "-s -w -X github.com/proofgate/proofgate/internal/version.Version=${VERSION}" \
    -o /out/ ./cmd/proofgate ./cmd/proofgatectl

FROM gcr.io/distroless/static-debian12:nonroot@sha256:6ec7f65401662991cf9dc297e68cfb940026e642571ef5eb38cf0014798e1687
COPY --from=build /out/ /usr/local/bin/
COPY deploy/proofgate.yaml /etc/proofgate/proofgate.yaml
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/proofgate"]
