# Multi-stage build for hikvision-doorbell-server
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o doorbell-server ./cmd/server

# Final stage - scratch (minimal)
FROM scratch

WORKDIR /app

# Copy binary
COPY --from=builder /build/doorbell-server .

# Expose port
EXPOSE 8080

# Run the server
ENTRYPOINT ["/app/doorbell-server"]
CMD ["-config", "/app/config.yaml"]
