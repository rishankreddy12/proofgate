FROM golang:latest AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/proofgate/proofgate/internal/version.Version=${VERSION}" \
    -o /out/ ./cmd/proofgate ./cmd/proofgatectl

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
COPY deploy/proofgate.yaml /etc/proofgate/proofgate.yaml
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/proofgate"]
