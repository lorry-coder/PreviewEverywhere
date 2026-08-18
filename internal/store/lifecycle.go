package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// 文档的两个生命周期动作：删除，和改名跟随。
//
// 它们是同一处缺口的两面。平台是文件系统的下游，所以源文件消失时文档照留——
// 这条取舍本身是对的（你读过、标注过的东西不该因为别人清理了磁盘就没了），
// 但它有两个必须补上的代价：
//
//  1. 改名在文件事件里等于「删旧 + 建新」，不做处理就会留下两篇一模一样的文档，
//     而且批注、标签、已读状态全留在那篇再也不会更新的旧的上。
//  2. 误采进来的东西没有任何清理手段。

// RenameCandidate 是一篇「内容和新文件一致、但自己的源文件可能已经不在了」的文档。
// 是否真的不在由调用方判断——store 不碰文件系统。
type RenameCandidate struct {
	DocID      int64
	SourceKey  string
	SourcePath string
}

// FindRenameCandidates 找出同项目内正文与 contentSha 相同的其它文档。
//
// 判定改名的完整条件是「内容一致 + 旧路径已不存在 + 新 source_key 尚未入库」，
// 这里只负责前半句。用内容哈希而不是标题或体积：哈希相同基本排除了巧合，
// 而改名恰恰是内容一字不变的那种操作。
func (s *Store) FindRenameCandidates(projectID int64, contentSha, excludeKey string) ([]RenameCandidate, error) {
	rows, err := s.DB.Query(`
		SELECT d.id, d.source_key, COALESCE(d.source_path, '')
		  FROM doc d JOIN doc_version v ON v.id = d.head_version
		 WHERE d.project_id = ? AND v.content_sha = ? AND d.source_key <> ?`,
		projectID, contentSha, excludeKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RenameCandidate
	for rows.Next() {
		var c RenameCandidate
		if err := rows.Scan(&c.DocID, &c.SourceKey, &c.SourcePath); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RelocateDoc 把一篇文档挪到新的 source_key / source_path 上。
//
// 这就是「改名跟随」的落地动作：文档行不变，所以批注、标签、已读状态、
// 版本历史全都原地留着——改名本来就不该让你损失任何东西。
func (s *Store) RelocateDoc(docID int64, sourceKey, sourcePath string) error {
	_, err := s.DB.Exec(`
		UPDATE doc SET source_key = ?, source_path = ?, updated_at = ?
		 WHERE id = ?`,
		sourceKey, nullIfEmpty(sourcePath), time.Now().Unix(), docID)
	if err != nil {
		return fmt.Errorf("改名跟随失败: %w", err)
	}
	return nil
}

// DeleteDoc 删除一篇文档，并按需留下墓碑。
//
// doc_version / doc_tag / annotation 靠外键级联跟着走，但 doc_fts 不行：
// 它是普通的 FTS5 表，没有触发器，由代码维护。漏删这一行的后果是留下一条
// 搜得到却打不开的幽灵记录——比留着原文档更糟。
//
// blob 一概不动。同一份内容可能被多篇文档共用（同样的图片、改名前后的两个版本），
// 没有引用计数就删是数据损坏，不是节省空间。
func (s *Store) DeleteDoc(docID int64, tombstone bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var projectID int64
	var sourceKey, title string
	err = tx.QueryRow(`SELECT project_id, source_key, title FROM doc WHERE id = ?`, docID).
		Scan(&projectID, &sourceKey, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if tombstone {
		if _, err := tx.Exec(`
			INSERT INTO deleted_source (project_id, source_key, title, deleted_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(project_id, source_key) DO UPDATE SET deleted_at = excluded.deleted_at`,
			projectID, sourceKey, title, time.Now().Unix()); err != nil {
			return fmt.Errorf("记录删除标记失败: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM doc_fts WHERE rowid = ?`, docID); err != nil {
		return fmt.Errorf("清理全文索引失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM doc WHERE id = ?`, docID); err != nil {
		return fmt.Errorf("删除文档失败: %w", err)
	}
	return tx.Commit()
}

// IsDeleted 判断某个来源是否被主动删除过。
// 自动通道（监听、hook）应当尊重它；显式的 pe push 则应当先调 ClearDeleted——
// 「你亲手推的」比「你几个月前删过」更能代表当下的意图。
func (s *Store) IsDeleted(projectID int64, sourceKey string) (bool, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM deleted_source WHERE project_id = ? AND source_key = ?`,
		projectID, sourceKey).Scan(&n)
	return n > 0, err
}

// ClearDeleted 撤掉墓碑，让这个来源可以重新被自动采集。
func (s *Store) ClearDeleted(projectID int64, sourceKey string) error {
	_, err := s.DB.Exec(
		`DELETE FROM deleted_source WHERE project_id = ? AND source_key = ?`,
		projectID, sourceKey)
	return err
}
