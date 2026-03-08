FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o mnemo-server ./cmd/mnemo-server

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/mnemo-server /usr/local/bin/
EXPOSE 8080
CMD ["mnemo-server"]
