# The image is the binary. No shell, no package manager, no base OS to patch: Google's
# distroless static image adds only CA certificates (TLS for outbound webhooks), tzdata and
# a nonroot passwd entry on top of what scratch had.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
# Set by buildx. Building natively and cross-compiling avoids QEMU entirely.
ARG TARGETOS TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/bettersentryio ./cmd/bettersentryio

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/bettersentryio /bettersentryio

# :nonroot already runs as 65532; kept explicit.
USER 65532:65532
EXPOSE 9090

ENTRYPOINT ["/bettersentryio"]
CMD ["serve"]
