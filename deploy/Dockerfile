# Refresh digests with: docker buildx imagetools inspect golang:1.25 --format '{{json .Manifest.Digest}}'
FROM golang:1.25@sha256:699337d620559a59b4a2bb298ad59611e535d2ee755a34cf2d2a98f37578dc80 AS build
WORKDIR /src
COPY . .
ARG VERSION=dev
RUN if [ -f deploy/bin/proofgate ] && [ -f deploy/bin/proofgatectl ]; then \
      mkdir -p /out && cp deploy/bin/proofgate deploy/bin/proofgatectl /out/; \
    else \
      go mod download && \
      CGO_ENABLED=0 GOFLAGS=-mod=readonly go build -trimpath -buildvcs=true \
        -ldflags "-s -w -X github.com/proofgate/proofgate/internal/version.Version=${VERSION}" \
        -o /out/ ./cmd/proofgate ./cmd/proofgatectl; \
    fi

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/ /usr/local/bin/
COPY deploy/proofgate.yaml /etc/proofgate/proofgate.yaml
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/proofgate"]
