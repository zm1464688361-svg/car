package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildHandler(t *testing.T) {
	dir := t.TempDir()
	h, db, stop, err := buildHandler(filepath.Join(dir, "t.db"), "test-secret")
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	defer close(stop)
	defer db.Close()

	// 登录页可访问
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("登录页应 200: %d", resp.StatusCode)
	}
}

func TestBuildHandlerInvalidPath(t *testing.T) {
	// 跨平台：指向不存在目录的路径，Windows/Linux 均无法打开数据库
	if _, _, _, err := buildHandler(filepath.Join(t.TempDir(), "no", "such", "dir", "x.db"), "s"); err == nil {
		t.Fatal("非法数据库路径应报错")
	}
}

func TestAckTimeoutEnv(t *testing.T) {
	os.Setenv("ACK_TIMEOUT", "5")
	if ackTimeout() != 5*time.Second {
		t.Fatalf("应读取 ACK_TIMEOUT=5: %v", ackTimeout())
	}
	os.Setenv("ACK_TIMEOUT", "bad")
	if ackTimeout() != 60*time.Second {
		t.Fatal("非法值应回退 60s")
	}
	os.Unsetenv("ACK_TIMEOUT")
	if ackTimeout() != 60*time.Second {
		t.Fatal("缺省应为 60s")
	}
}

func TestGetenv(t *testing.T) {
	os.Setenv("TEST_XYZ", "v")
	if getenv("TEST_XYZ", "d") != "v" {
		t.Fatal("应读取环境变量")
	}
	os.Unsetenv("TEST_XYZ")
	if getenv("TEST_XYZ", "d") != "d" {
		t.Fatal("缺省应返回默认值")
	}
}
