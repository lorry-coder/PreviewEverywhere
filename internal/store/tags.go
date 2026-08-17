package store

import (
	"database/sql"
	"strings"
	"time"
)

// SetDocTags 把一篇文档的可见标签替换成给定集合，返回替换后的结果。
//
// 关键在于「删掉一个来自 front-matter 的标签」该怎么处理：直接删行的话，
// agent 下次重新生成这篇文档时，front-matter 里那个标签又会冒出来。
// 所以这里留一条 source='blocked' 的墓碑，采集管线看到它就不再写回。
func (s *Store) SetDocTags(docID int64, tags []string) ([]string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current := map[string]string{} // 名称 → source
	rows, err := tx.Query(`
		SELECT t.name, dt.source FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		 WHERE dt.doc_id = ? AND dt.source <> 'blocked'`, docID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, source string
		if err := rows.Scan(&name, &source); err != nil {
			rows.Close()
			return nil, err
		}
		current[name] = source
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	want := map[string]bool{}
	ordered := []string{}
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && !want[t] {
			want[t] = true
			ordered = append(ordered, t)
		}
	}

	for name, source := range current {
		if want[name] {
			continue
		}
		if source == "push" {
			// 来自 front-matter：留墓碑，别让 agent 重新生成时又加回来。
			if _, err := tx.Exec(`
				UPDATE doc_tag SET source = 'blocked'
				 WHERE doc_id = ? AND tag_id = (SELECT id FROM tag WHERE name = ?)`,
				docID, name); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.Exec(`
			DELETE FROM doc_tag
			 WHERE doc_id = ? AND tag_id = (SELECT id FROM tag WHERE name = ?)`,
			docID, name); err != nil {
			return nil, err
		}
	}

	for _, name := range ordered {
		if _, ok := current[name]; ok {
			continue
		}
		tagID, err := ensureTag(tx, name)
		if err != nil {
			return nil, err
		}
		// 可能命中一条墓碑行，这时把它翻回 manual。
		if _, err := tx.Exec(`
			INSERT INTO doc_tag (doc_id, tag_id, source) VALUES (?, ?, 'manual')
			ON CONFLICT(doc_id, tag_id) DO UPDATE SET source = 'manual'`,
			docID, tagID); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(`UPDATE doc SET updated_at = updated_at WHERE id = ?`, docID); err != nil {
		return nil, err
	}
	if err := reindexDoc(tx, docID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// 顺手清掉不再被任何文档引用的标签，免得侧栏堆满一次性标签。
	s.DB.Exec(`DELETE FROM tag WHERE id NOT IN (SELECT tag_id FROM doc_tag)`)
	return ordered, nil
}

func ensureTag(tx *sql.Tx, name string) (int64, error) {
	if _, err := tx.Exec(`INSERT INTO tag (name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM tag WHERE name = ?`, name).Scan(&id)
	return id, err
}

// RenameTag 全局重命名一个标签。合并到已存在的标签时按 doc 去重。
func (s *Store) RenameTag(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" || newName == oldName {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var oldID int64
	if err := tx.QueryRow(`SELECT id FROM tag WHERE name = ?`, oldName).Scan(&oldID); err != nil {
		return err
	}
	newID, err := ensureTag(tx, newName)
	if err != nil {
		return err
	}
	if newID == oldID {
		return tx.Commit()
	}
	if _, err := tx.Exec(`
		INSERT INTO doc_tag (doc_id, tag_id, source)
		SELECT doc_id, ?, source FROM doc_tag WHERE tag_id = ?
		ON CONFLICT(doc_id, tag_id) DO NOTHING`, newID, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_tag WHERE tag_id = ?`, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tag WHERE id = ?`, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE doc SET updated_at = ? WHERE id IN (SELECT doc_id FROM doc_tag WHERE tag_id = ?)`,
		time.Now().Unix(), newID); err != nil {
		return err
	}
	return tx.Commit()
}

// reindexDoc 只重建 FTS 里的标签列，正文与标题从当前版本读回。
func reindexDoc(tx *sql.Tx, docID int64) error {
	var title, plain string
	err := tx.QueryRow(`
		SELECT d.title, COALESCE(v.plain, '') FROM doc d
		  LEFT JOIN doc_version v ON v.id = d.head_version WHERE d.id = ?`, docID).Scan(&title, &plain)
	if err != nil {
		return err
	}
	return reindex(tx, docID, title, plain)
}
