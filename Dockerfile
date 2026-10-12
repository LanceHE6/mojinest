# 极简运行镜像：直接复用本地编译好的静态二进制，毫秒级构建
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY mojinest /usr/local/bin/mojinest
EXPOSE 18642
ENTRYPOINT ["mojinest"]
