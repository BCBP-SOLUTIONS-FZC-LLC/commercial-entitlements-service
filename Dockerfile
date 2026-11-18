# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.26.11-alpine AS builder

WORKDIR /app

# Private module credentials — supply via Docker secret in CI.
ARG GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*
ARG GONOSUMDB=github.com/BCBP-SOLUTIONS-FZC-LLC/*
ARG GOPROXY=direct

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GOPRIVATE=${GOPRIVATE}
ENV GONOSUMDB=${GONOSUMDB}
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN --mount=type=secret,id=go_private_token \
    if [ -f /run/secrets/go_private_token ]; then \
      git config --global url."https://oauth2:$(cat /run/secrets/go_private_token)@github.com/".insteadOf "https://github.com/"; \
    fi && \
    go mod download

COPY . .

RUN BUILD_VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev) && \
    go build -ldflags "-s -w -X main.buildVersion=${BUILD_VERSION}" -o bin/server           ./cmd/server && \
    go build -ldflags "-s -w -X main.buildVersion=${BUILD_VERSION}" -o bin/reconciler       ./cmd/reconciler && \
    go build -ldflags "-s -w"                                        -o bin/migrate         ./cmd/migrate && \
    go build -ldflags "-s -w"                                        -o bin/worker          ./cmd/worker && \
    go build -ldflags "-s -w"                                        -o bin/expiry          ./cmd/expiry && \
    go build -ldflags "-s -w"                                        -o bin/catalog-refresh ./cmd/catalog-refresh

# ── Runtime stage (distroless) ───────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=builder /app/bin/server           /app/server
COPY --from=builder /app/bin/reconciler       /app/reconciler
COPY --from=builder /app/bin/migrate          /app/migrate
COPY --from=builder /app/bin/worker           /app/worker
COPY --from=builder /app/bin/expiry           /app/expiry
COPY --from=builder /app/bin/catalog-refresh  /app/catalog-refresh

EXPOSE 8080 9090

USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
