package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openSeed(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Seed(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func TestOpenMigrateSeed(t *testing.T) {
	dir := t.TempDir()
	db := openSeed(t, filepath.Join(dir, "t.db"))

	// 迁移与预置幂等：重复执行不报错、不重复插入
	if err := Migrate(db); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if err := Seed(db); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("应 4 个账号，实际 %d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("应 5 台车，实际 %d err=%v", n, err)
	}

	// 预置数据内容校验：V1 在线、V2 离线、初始间隔 60
	var online int
	var interval int
	if err := db.QueryRow(`SELECT online, current_interval FROM vehicles WHERE id='V1'`).Scan(&online, &interval); err != nil {
		t.Fatal(err)
	}
	if online != 1 || interval != 60 {
		t.Fatalf("V1 预置异常: online=%d interval=%d", online, interval)
	}
	if err := db.QueryRow(`SELECT online FROM vehicles WHERE id='V2'`).Scan(&online); err != nil || online != 0 {
		t.Fatalf("V2 应离线: online=%d err=%v", online, err)
	}
}

func TestOpenInvalidPath(t *testing.T) {
	// 非法路径应返回错误而不是 panic
	if _, err := Open(`\\\\invalid\\path\\x.db`); err == nil {
		t.Fatal("非法路径应报错")
	}
}

func TestSeedBadSchema(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// 与预置表结构冲突的 users 表：Seed 插入应失败而非静默成功
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := Seed(db); err == nil {
		t.Fatal("表结构冲突时 Seed 应失败")
	}
}
