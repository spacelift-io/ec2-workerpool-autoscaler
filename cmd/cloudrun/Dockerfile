# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git for private modules if needed
RUN apk add --no-cache git

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-w -s" -o autoscaler ./cmd/cloudrun

# Runtime stage - use distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/autoscaler /app/autoscaler

# Use non-root user (provided by distroless:nonroot)
USER nonroot:nonroot

# Cloud Run sets PORT environment variable
ENV PORT=8080

# Expose the port
EXPOSE 8080

# Run the binary
ENTRYPOINT ["/app/autoscaler"]
