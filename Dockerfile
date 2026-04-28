FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o ado-token ./cmd

# distroless/static-nonroot includes CA certificates (required for AAD HTTPS)
# and runs as uid 65532 (nonroot) by default.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /build/ado-token /ado-token
ENTRYPOINT ["/ado-token"]
