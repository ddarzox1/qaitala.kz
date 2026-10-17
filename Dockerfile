FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /qaitala ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -o /seedclips ./cmd/seedclips

FROM alpine:3.21
# ffmpeg нужен и на приёме фото (айфоны шлют HEIC, Higgsfield его не берёт),
# и в seedclips для нормализации роликов.
RUN apk add --no-cache ca-certificates tzdata ffmpeg
WORKDIR /app
COPY --from=builder /qaitala /seedclips ./
EXPOSE 8080
CMD ["./qaitala"]
