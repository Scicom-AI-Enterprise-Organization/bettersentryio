# The image is the binary. No shell, no package manager, no base OS to patch —
# the only reason anything else is copied in is TLS trust for outbound webhooks.
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/bettersentryio ./cmd/bettersentryio

FROM scratch

# Needed for HTTPS delivery to Slack / Teams / Telegram.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/bettersentryio /bettersentryio

# Non-root by uid: scratch has no /etc/passwd to name a user in.
USER 65532:65532
EXPOSE 9090

ENTRYPOINT ["/bettersentryio"]
CMD ["serve"]
