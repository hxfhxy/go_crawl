package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // 匿名导入：触发驱动的 init() 自动注册 "sqlite"

	"gocrawl/parser"
)

type SQLiteStore struct {
	db *sql.DB
}

// 编译期检查：确保 SQLiteStore 严格实现了 Store 接口
var _ Store = (*SQLiteStore)(nil)

// OpenSQLite 打开（或创建）数据库文件并初始化表结构
func OpenSQLite(dbPath string) (*SQLiteStore, error) {
	// 1. 打开数据库连接
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 推荐优化配置：开启 WAL 模式提升并发读写性能，避免 "database is locked"
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("开启 WAL 模式失败: %w", err)
	}

	// 2. 建表（如果不存在）
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS pages (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		url        TEXT UNIQUE NOT NULL,
		title      TEXT,
		text       TEXT,
		crawled_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化建表失败: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Save 写入抓取结果，使用 INSERT OR REPLACE 保证 URL 唯一性约束下可重复写入
func (s *SQLiteStore) Save(p *parser.Page) error {
	query := `
	INSERT OR REPLACE INTO pages (url, title, text, crawled_at)
	VALUES (?, ?, ?, CURRENT_TIMESTAMP);
	`
	// 必须使用占位符参数绑定，防止 SQL 注入并复用预编译语句
	_, err := s.db.Exec(query, p.URL, p.Title, p.Text)
	if err != nil {
		return fmt.Errorf("保存页面失败 [%s]: %w", p.URL, err)
	}
	return nil
}

// Seen 查询该 URL 是否存在于数据库中（用于增量爬取）
func (s *SQLiteStore) Seen(url string) (bool, error) {
	query := `SELECT 1 FROM pages WHERE url = ? LIMIT 1;`

	var dummy int
	err := s.db.QueryRow(query, url).Scan(&dummy)
	if err == sql.ErrNoRows {
		// 查不到说明没爬过
		return false, nil
	}
	if err != nil {
		// 底层数据库故障
		return false, fmt.Errorf("查询 URL 状态失败: %w", err)
	}

	// 查到了说明已存在
	return true, nil
}

// Close 关闭底层数据库连接池
func (s *SQLiteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
