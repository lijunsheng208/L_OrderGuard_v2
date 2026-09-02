# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder

WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

ARG TARGET
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/orderguard "./cmd/${TARGET}"

FROM alpine:3.23
RUN addgroup -S orderguard && adduser -S orderguard -G orderguard
COPY --from=builder /out/orderguard /usr/local/bin/orderguard
USER orderguard
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/orderguard"]
