# Build context is snaphost-backend/ — the module root (Task 1 item 4).
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api ./ai-orchestrator/cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /migrate ./ai-orchestrator/cmd/migrate

FROM gcr.io/distroless/static-debian12
COPY --from=builder /api /api
COPY --from=builder /migrate /migrate
EXPOSE 8083
CMD ["/api"]
