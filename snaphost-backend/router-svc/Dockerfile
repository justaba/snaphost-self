# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build/router-svc

# Build context must be snaphost-backend/ so ../shared is available for the
# router-svc go.mod replace directive.
COPY shared/ ../shared/
COPY router-svc/go.mod router-svc/go.sum ./
RUN go mod download

COPY router-svc/ ./
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/router-svc .

FROM debian:12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/router-svc /router-svc

EXPOSE 8085

CMD ["/router-svc"]
