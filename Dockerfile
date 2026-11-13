# 构建阶段：纯 Go 交叉编译，无 CGO 依赖
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/server ./cmd/server

# 运行阶段：最小镜像
FROM alpine:3.20
RUN adduser -D -u 10001 app && mkdir -p /data && chown app /data
USER app
WORKDIR /app
COPY --from=build /out/server ./server
ENV DB_PATH=/data/data.db \
    ADDR=:8080 \
    SESSION_SECRET=teaching-secret-change-me
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["./server"]
