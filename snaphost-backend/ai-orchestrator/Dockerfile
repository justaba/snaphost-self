FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY shared/ ../shared/
COPY ai-orchestrator/ ./
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12
COPY --from=builder /api /api
COPY --from=builder /migrate /migrate
EXPOSE 8083
CMD ["/api"]
