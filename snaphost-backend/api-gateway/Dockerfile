FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o api-gateway .

FROM gcr.io/distroless/static-debian12

WORKDIR /
COPY --from=builder /build/api-gateway /api-gateway
COPY rbac_model.conf /rbac_model.conf
COPY rbac_policy.csv /rbac_policy.csv

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/api-gateway"]
