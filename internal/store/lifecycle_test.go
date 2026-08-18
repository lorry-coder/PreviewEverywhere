package store

import "testing"

// ── 测试脚手架 ────────────────────────────────────────────────────

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedDoc 造一篇「什么都齐」的文档：有版本、有标签、有批注、进了全文索引。
// 删除与改名的测试都得靠这些附属数据才验得出问题。
func seedDoc(t *testing.T, s *Store, sourceKey, plain string) int64 {
	t.Helper()
	projectID, err := s.EnsureProject("t", "测试项目", "/tmp/t")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := s.PutBlob([]byte(plain), "text/markdown; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.SaveDoc(SaveDocInput{
		ProjectID:  projectID,
		SourceKey:  sourceKey,
		SourcePath: "/tmp/t/" + sourceKey,
		Title:      sourceKey,
		Kind:       "markdown",
		ContentSha: Sha256Hex([]byte(plain)),
		RawBlob:    blob,
		HTML:       `<p data-blk="blk00001">` + plain + `</p>`,
		Plain:      plain,
		TOC:        "[]",
		Blocks:     `[{"b":"blk00001","o":0,"l":` + itoa64(int64(len([]rune(plain)))) + `}]`,
		Chars:      len([]rune(plain)),
		Bytes:      len(plain),
		Tags:       []string{"测试标签"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAnnotation(NewAnnotation{
		DocID: res.DocID, Kind: "highlight", Blk: "blk00001",
		StartOff: 0, EndOff: 2, Exact: string([]rune(plain)[:2]),
	}); err != nil {
		t.Fatal(err)
	}
	return res.DocID
}

// 删除必须把 doc_fts 里的那一行一起带走。FTS 表没有触发器、由代码维护，
// 漏删的后果是留下一条搜得到却打不开的幽灵记录——比不删更糟。
func TestDeleteDocClearsFTS(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "甲.md", "关于索引一致性的说明")

	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM doc_fts WHERE doc_fts MATCH ?`, "索引一致性").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("准备阶段就该能搜到，实得 %d", n)
	}

	if err := s.DeleteDoc(docID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM doc_fts WHERE doc_fts MATCH ?`, "索引一致性").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("删除后仍能搜到，doc_fts 没被清理")
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM doc WHERE id = ?`, docID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("doc 行没被删掉")
	}
}

// 级联：批注、标签关联、版本都该跟着走，否则库里会攒下永远查不到的孤儿行。
func TestDeleteDocCascades(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "乙.md", "正文")

	for _, table := range []string{"doc_version", "doc_tag", "annotation"} {
		var before int
		s.DB.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE doc_id = ` + itoa64(docID)).Scan(&before)
		if table == "doc_version" && before == 0 {
			t.Fatalf("准备阶段 %s 就该有数据", table)
		}
	}
	if err := s.DeleteDoc(docID, false); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"doc_version", "doc_tag", "annotation"} {
		var after int
		if err := s.DB.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE doc_id = ` + itoa64(docID)).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != 0 {
			t.Errorf("%s 里还剩 %d 条孤儿行", table, after)
		}
	}
}

// 墓碑：删过的来源，自动通道不该再收；显式推送则应当覆盖它。
func TestTombstone(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "丙.md", "正文")

	var projectID int64
	if err := s.DB.QueryRow(`SELECT project_id FROM doc WHERE id = ?`, docID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDoc(docID, true); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.IsDeleted(projectID, "丙.md")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("留了墓碑却查不到")
	}

	if err := s.ClearDeleted(projectID, "丙.md"); err != nil {
		t.Fatal(err)
	}
	if deleted, _ = s.IsDeleted(projectID, "丙.md"); deleted {
		t.Error("显式推送后墓碑该被清掉")
	}
}

// 不留墓碑的删除（forget）不该挡住以后的自动采集。
func TestDeleteWithoutTombstone(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "丁.md", "正文")
	var projectID int64
	s.DB.QueryRow(`SELECT project_id FROM doc WHERE id = ?`, docID).Scan(&projectID)

	if err := s.DeleteDoc(docID, false); err != nil {
		t.Fatal(err)
	}
	if deleted, _ := s.IsDeleted(projectID, "丁.md"); deleted {
		t.Error("forget 模式不该留墓碑")
	}
}

func TestDeleteMissingDoc(t *testing.T) {
	s := testStore(t)
	if err := s.DeleteDoc(99999, true); err != ErrNotFound {
		t.Errorf("删不存在的文档该返回 ErrNotFound，实得 %v", err)
	}
}

// 改名跟随的前半句：按内容哈希找出同项目内的同内容文档。
func TestFindRenameCandidates(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "原名.md", "一模一样的正文")
	var projectID int64
	var sha string
	s.DB.QueryRow(`SELECT d.project_id, v.content_sha FROM doc d
	                 JOIN doc_version v ON v.id = d.head_version WHERE d.id = ?`, docID).
		Scan(&projectID, &sha)

	got, err := s.FindRenameCandidates(projectID, sha, "新名.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DocID != docID {
		t.Fatalf("应当找到原文档，实得 %+v", got)
	}
	// 排除自己：source_key 相同的不是改名
	if got, _ := s.FindRenameCandidates(projectID, sha, "原名.md"); len(got) != 0 {
		t.Error("同名的不该算作改名候选")
	}
}

// 改名跟随的落地动作：文档行不变，所以批注和已读状态原地留着。
func TestRelocateDocKeepsAnnotations(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "原名.md", "正文")

	var before int
	s.DB.QueryRow(`SELECT count(*) FROM annotation WHERE doc_id = ?`, docID).Scan(&before)

	if err := s.RelocateDoc(docID, "新名.md", "/tmp/新名.md"); err != nil {
		t.Fatal(err)
	}

	var key, path string
	if err := s.DB.QueryRow(`SELECT source_key, source_path FROM doc WHERE id = ?`, docID).
		Scan(&key, &path); err != nil {
		t.Fatal(err)
	}
	if key != "新名.md" || path != "/tmp/新名.md" {
		t.Errorf("路径没更新: %s / %s", key, path)
	}
	var after int
	s.DB.QueryRow(`SELECT count(*) FROM annotation WHERE doc_id = ?`, docID).Scan(&after)
	if after != before {
		t.Errorf("改名不该动批注：改名前 %d 条，改名后 %d 条", before, after)
	}
}
