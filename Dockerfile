# 构建阶段
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 复制源码
COPY . .

# 编译为静态、可复现性更好的发布二进制
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o fntv-proxy ./cmd/main.go

# 运行阶段
FROM alpine:3.22

WORKDIR /app

# 安装 ca-certificates 和 tzdata（时区数据）
RUN apk --no-cache add ca-certificates tzdata

# 设置时区为 Asia/Shanghai（东八区）
ENV TZ=Asia/Shanghai
RUN cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

# 复制二进制文件
COPY --from=builder /app/fntv-proxy /app/fntv-proxy

# 复制默认配置文件（仓库内为 config.yaml.example）
COPY --from=builder /app/config.yaml.example /app/config.yaml

# 声明端口不代表启用服务：49963 仅 role=media/all 时监听
EXPOSE 28005 8095 49963

# 运行
ENTRYPOINT ["/app/fntv-proxy"]
