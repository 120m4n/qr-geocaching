# Official Go Apline Base Image
FROM golang:1.21.0-alpine AS builder

# Create The Application Directory
WORKDIR /app

# Copy and Download Dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy The Application Source & Build
COPY . .
RUN go build -o main .

# Final Image Creation Stage
FROM alpine:3.20

WORKDIR /root/

# Copy The Built Binary
COPY --from=builder /app/main .
COPY ./assets ./assets

# Expose the port
EXPOSE 3080
CMD ["./main"]