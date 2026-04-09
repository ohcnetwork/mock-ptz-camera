FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o mock-ptz-camera .

FROM alpine:3.23

RUN apk add --no-cache ffmpeg

WORKDIR /app
COPY --from=builder /app/mock-ptz-camera .

EXPOSE 8554 8080 3702/udp

ENV CAMERA_USER=admin
ENV CAMERA_PASS=admin
ENV RTSP_PORT=8554
ENV WEB_PORT=8080

ENTRYPOINT ["./mock-ptz-camera"]
