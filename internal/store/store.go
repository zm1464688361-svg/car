// Package store 负责 SQLite 打开、迁移与预置数据。
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open 打开（必要时创建）SQLite 数据库并设置连接参数。
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite 写入串行化，单连接避免锁竞争
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// Migrate 建表（幂等）。
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS vehicles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			online INTEGER NOT NULL DEFAULT 1,
			current_version INTEGER NOT NULL DEFAULT 0,
			current_interval INTEGER NOT NULL,
			baseline INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS activities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			applicant_id INTEGER NOT NULL,
			approver_id INTEGER,
			reason TEXT NOT NULL,
			interval INTEGER NOT NULL,
			status TEXT NOT NULL,
			reject_reason TEXT,
			pilot_ids TEXT NOT NULL DEFAULT '[]',
			stop_reason TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS publishes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			activity_id INTEGER NOT NULL REFERENCES activities(id),
			vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
			version INTEGER NOT NULL,
			target INTEGER NOT NULL,
			batch TEXT NOT NULL,
			status TEXT NOT NULL,
			published_at TEXT NOT NULL,
			ack_at TEXT,
			fail_reason TEXT,
			timeout_at TEXT,
			late INTEGER NOT NULL DEFAULT 0,
			UNIQUE(activity_id, vehicle_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			vehicle_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			result TEXT NOT NULL,
			accepted INTEGER NOT NULL,
			code TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			activity_id INTEGER NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pub_vehicle ON publishes(vehicle_id, version)`,
		`CREATE INDEX IF NOT EXISTS idx_pub_activity ON publishes(activity_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Seed 预置角色账号与 5 台测试车（幂等）。
func Seed(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		users := []struct{ name, role string }{
			{"张研发", "applicant"}, {"赵研发", "applicant"},
			{"李负责", "approver"}, {"王运营", "operator"},
		}
		for _, u := range users {
			if _, err := db.Exec(`INSERT INTO users(name, role) VALUES(?, ?)`, u.name, u.role); err != nil {
				return err
			}
		}
	}
	var v int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles`).Scan(&v); err != nil {
		return err
	}
	if v == 0 {
		// V1 在线、V2 离线（演示离线分支），其余在线；初始间隔 60 秒
		onlines := map[string]bool{"V1": true, "V2": false, "V3": true, "V4": true, "V5": true}
		for i := 1; i <= 5; i++ {
			id := fmt.Sprintf("V%d", i)
			online := 0
			if onlines[id] {
				online = 1
			}
			if _, err := db.Exec(
				`INSERT INTO vehicles(id, name, token, online, current_version, current_interval) VALUES(?,?,?,?,0,?)`,
				id, "测试车-"+id, "vtok-"+id, online, 60); err != nil {
				return err
			}
		}
	}
	return nil
}
