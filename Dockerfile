# Multi-stage Dockerfile for Go backend

FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

ARG BACKEND_MAIN_TARGET=./cmd/server/main.go

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOMAXPROCS=2 GOMEMLIMIT=700MiB go build -trimpath -ldflags="-s -w" -o server "$BACKEND_MAIN_TARGET"

# Final stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates binutils docker-cli gdb

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/server .

EXPOSE 8080

ENV PORT=8080
ENV BASE_IMAGE_URL="zephyr-emulator:latest"
ENV RUNTIME_NAME="runsc"

CMD ["./server"]
