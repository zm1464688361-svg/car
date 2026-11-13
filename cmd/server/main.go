// 命令 server：车队日志策略发布平台（题目09）。
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/zm1464688361-svg/car/internal/service"
	"github.com/zm1464688361-svg/car/internal/store"
	"github.com/zm1464688361-svg/car/internal/web"
)

func main() {
	dbPath := getenv("DB_PATH", "data.db")
	addr := getenv("ADDR", ":8080")
	secret := getenv("SESSION_SECRET", "teaching-secret-change-me")

	handler, db, stop, err := buildHandler(dbPath, secret)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
	defer db.Close()
	defer close(stop)

	log.Printf("车队日志策略发布平台已启动 http://localhost%s（DB=%s）", addr, dbPath)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

// buildHandler 组装数据库、服务与路由（可测试的入口装配）。
func buildHandler(dbPath, secret string) (http.Handler, *sql.DB, chan struct{}, error) {
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := store.Migrate(db); err != nil {
		db.Close()
		return nil, nil, nil, err
	}
	if err := store.Seed(db); err != nil {
		db.Close()
		return nil, nil, nil, err
	}
	svc := service.New(db)
	svc.Timeout = ackTimeout()
	stop := svc.StartTimeoutTicker(1 * time.Second)
	srv, err := web.NewServer(svc, secret)
	if err != nil {
		db.Close()
		close(stop)
		return nil, nil, nil, err
	}
	return srv.Handler(), db, stop, nil
}

// ackTimeout 回执超时秒数，题面默认 60；教学可用 ACK_TIMEOUT 缩短。
func ackTimeout() (d time.Duration) {
	d = 60 * time.Second
	if v := os.Getenv("ACK_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			d = time.Duration(n) * time.Second
		}
	}
	return d
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
