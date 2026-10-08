FROM golang:1.27-alpine AS build
ENV GOPROXY=https://goproxy.cn,direct CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/mojinest ./cmd/mojinest

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 mojinest
WORKDIR /app
COPY --from=build /out/mojinest /usr/local/bin/mojinest
USER mojinest
ENV MOJINEST_DATA_DIR=/data MOJINEST_LISTEN=:8642
EXPOSE 8642
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8642/healthz || exit 1
ENTRYPOINT ["mojinest"]
