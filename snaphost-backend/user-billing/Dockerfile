# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1: Build
# ---------------------------------------------------------------------------
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o user-billing . && \
    CGO_ENABLED=0 go build -ldflags='-s -w' -o migrate ./cmd/migrate

# ---------------------------------------------------------------------------
# Stage 2: Runtime (distroless — minimal attack surface)
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12

COPY --from=builder /build/user-billing /user-billing
COPY --from=builder /build/migrate /migrate

EXPOSE 8081

USER nonroot:nonroot

ENTRYPOINT ["/user-billing"]
