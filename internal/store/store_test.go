package store

import (
	"testing"
)

// 这个测试的真正目的是尽早验证两件容易踩空的事：
// 1. modernc.org/sqlite 这个纯 Go 驱动确实编进了 FTS5；
// 2. FTS5 的 trigram 分词器可用，且中文子串检索按预期工作。
// 任何一条不成立，07 章的检索方案都要换，越早发现越好。
func TestSchemaAndChineseFTS(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	defer s.Close()

	// 版本号应当等于迁移脚本数量，而不是写死的常量——
	// 否则每加一个迁移都要来改这个断言。
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取 schema 版本失败: %v", err)
	}
	if version != len(entries) {
		t.Fatalf("schema 版本应等于迁移脚本数 %d，实得 %d", len(entries), version)
	}

	_, err = s.DB.Exec(
		`INSERT INTO doc_fts (rowid, title, plain, tags) VALUES (?, ?, ?, ?)`,
		1, "迁移风险评估", "双写窗口期若超过 30 分钟，订单表与快照表会出现不可收敛的偏差。", "风险 待复核")
	if err != nil {
		t.Fatalf("写入 FTS 失败: %v", err)
	}

	cases := []struct {
		query string
		want  int
		note  string
	}{
		{`"双写窗口期"`, 1, "中文子串"},
		{`"不可收敛"`, 1, "正文中段的中文子串"},
		{`"待复核"`, 1, "标签列"},
		{`"迁移风险"`, 1, "标题"},
		{`"根本不存在的词"`, 0, "不应误召回"},
	}
	for _, c := range cases {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM doc_fts WHERE doc_fts MATCH ?`, c.query).Scan(&n); err != nil {
			t.Fatalf("检索 %s 出错: %v", c.query, err)
		}
		if n != c.want {
			t.Errorf("%s：查询 %s 期望 %d 条，实得 %d", c.note, c.query, c.want, n)
		}
	}
}

func TestBlobRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	defer s.Close()

	data := []byte("# 标题\n\n正文")
	sha, err := s.PutBlob(data, "text/markdown")
	if err != nil {
		t.Fatalf("写入 blob 失败: %v", err)
	}
	// 内容寻址：同样内容重复写入应得到同一个 sha 且不报错。
	again, err := s.PutBlob(data, "text/markdown")
	if err != nil || again != sha {
		t.Fatalf("重复写入应幂等，得到 %q / %v", again, err)
	}

	got, err := s.ReadBlob(sha)
	if err != nil {
		t.Fatalf("读回 blob 失败: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("读回内容不一致: %q", got)
	}

	mime, path, err := s.BlobMeta(sha)
	if err != nil {
		t.Fatalf("读取 blob 元数据失败: %v", err)
	}
	if mime != "text/markdown" || path == "" {
		t.Fatalf("blob 元数据异常: mime=%q path=%q", mime, path)
	}
}
