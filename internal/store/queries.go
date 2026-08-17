package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("不存在")

// ── 项目 ──────────────────────────────────────────────────────────

// EnsureProject 按 slug 取项目，不存在则创建。采集管线每篇文档都会调它。
func (s *Store) EnsureProject(slug, name, rootPath string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM project WHERE slug = ?`, slug).Scan(&id)
	if err == nil {
		// 项目已存在但当初是通过推送创建的（没有 root_path），
		// 后来又配了监听目录时补上，让 UI 能显示来源。
		if rootPath != "" {
			s.DB.Exec(`UPDATE project SET root_path = ? WHERE id = ? AND (root_path IS NULL OR root_path = '')`, rootPath, id)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	res, err := s.DB.Exec(
		`INSERT INTO project (slug, name, root_path, created_at) VALUES (?, ?, ?, ?)`,
		slug, name, nullIfEmpty(rootPath), time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("创建项目失败: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.DB.Query(`
		SELECT p.id, p.slug, p.name, COALESCE(p.root_path, ''), COALESCE(p.color, ''), p.created_at,
		       COUNT(d.id),
		       COALESCE(SUM(CASE WHEN d.read_at IS NULL THEN 1 ELSE 0 END), 0)
		  FROM project p
		  LEFT JOIN doc d ON d.project_id = p.id
		 WHERE p.archived_at IS NULL
		 GROUP BY p.id
		 ORDER BY MAX(COALESCE(d.updated_at, p.created_at)) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.RootPath, &p.Color,
			&p.CreatedAt, &p.DocCount, &p.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── 文档写入 ──────────────────────────────────────────────────────

// HeadSha 返回文档当前版本的内容哈希，不存在时返回空串。
// 采集管线用它在渲染之前就挡掉没有变化的文件——agent 反复写同一份文档是常态，
// 这条快速路径省掉的是整条渲染管线的开销。
func (s *Store) HeadSha(projectID int64, sourceKey string) (string, error) {
	var sha sql.NullString
	err := s.DB.QueryRow(`
		SELECT v.content_sha FROM doc d
		  LEFT JOIN doc_version v ON v.id = d.head_version
		 WHERE d.project_id = ? AND d.source_key = ?`, projectID, sourceKey).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return sha.String, nil
}

// EnsureRun 按外部 ID（Claude Code 的 session_id 等）取或建一次运行记录。
// 它是首页时间线的骨架：你想看的往往是「昨晚那次跑出了什么」，
// 而不是「某个项目下的全部文档」。
func (s *Store) EnsureRun(projectID int64, externalID, label string) (int64, error) {
	if externalID == "" {
		return 0, nil
	}
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM run WHERE external_id = ?`, externalID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO run (project_id, external_id, label, started_at) VALUES (?, ?, ?, ?)`,
		projectID, externalID, nullIfEmpty(label), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const docSelect = `
	SELECT d.id, d.project_id, p.name, p.slug, d.source_key, COALESCE(d.source_path, ''),
	       d.title, COALESCE(d.summary, ''), d.kind, COALESCE(d.render_mode, ''),
	       COALESCE(v.seq, 0), COALESCE(v.chars, 0),
	       d.read_at IS NOT NULL, d.read_ratio, d.later,
	       d.created_at, d.updated_at
	  FROM doc d
	  JOIN project p ON p.id = d.project_id
	  LEFT JOIN doc_version v ON v.id = d.head_version`

func scanDoc(sc interface{ Scan(...any) error }) (Doc, error) {
	var d Doc
	err := sc.Scan(&d.ID, &d.ProjectID, &d.ProjectName, &d.ProjectSlug, &d.SourceKey,
		&d.SourcePath, &d.Title, &d.Summary, &d.Kind, &d.RenderMode, &d.Seq, &d.Chars,
		&d.Read, &d.ReadRatio, &d.Later, &d.CreatedAt, &d.UpdatedAt)
	d.Tags = []string{}
	return d, err
}

// SaveDoc 是采集管线唯一的写入口：在一个事务里完成去重判定、文档 upsert、
// 版本插入、head 指针更新、标签替换与 FTS 索引重建。
func (s *Store) SaveDoc(in SaveDocInput) (SaveResult, error) {
	var res SaveResult
	now := time.Now().Unix()

	tx, err := s.DB.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	var docID int64
	var headSha sql.NullString
	err = tx.QueryRow(`
		SELECT d.id, v.content_sha
		  FROM doc d LEFT JOIN doc_version v ON v.id = d.head_version
		 WHERE d.project_id = ? AND d.source_key = ?`,
		in.ProjectID, in.SourceKey).Scan(&docID, &headSha)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		r, err := tx.Exec(`
			INSERT INTO doc (project_id, source_key, source_path, title, summary, kind, render_mode, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.ProjectID, in.SourceKey, nullIfEmpty(in.SourcePath), in.Title,
			nullIfEmpty(in.Summary), in.Kind, nullIfEmpty(in.RenderMode), now, now)
		if err != nil {
			return res, fmt.Errorf("创建文档失败: %w", err)
		}
		if docID, err = r.LastInsertId(); err != nil {
			return res, err
		}
		res.NewDoc = true
	case err != nil:
		return res, err
	default:
		// 内容没变就到此为止。agent 反复写同一份文件是常态，这条判断挡掉了绝大多数无效工作。
		if headSha.Valid && headSha.String == in.ContentSha {
			res.DocID = docID
			return res, tx.Commit()
		}
	}
	res.DocID = docID

	var lastSeq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM doc_version WHERE doc_id = ?`, docID).Scan(&lastSeq); err != nil {
		return res, err
	}
	res.Seq = lastSeq + 1

	vr, err := tx.Exec(`
		INSERT INTO doc_version (doc_id, seq, content_sha, raw_blob, serve_blob, html, plain, toc, blocks, chars, bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		docID, res.Seq, in.ContentSha, in.RawBlob, nullIfEmpty(in.ServeBlob),
		in.HTML, in.Plain, in.TOC, in.Blocks, in.Chars, in.Bytes, now)
	if err != nil {
		return res, fmt.Errorf("写入文档版本失败: %w", err)
	}
	if res.VersionID, err = vr.LastInsertId(); err != nil {
		return res, err
	}

	// 新版本入库意味着「有新东西可读」，所以重置为未读。
	if _, err := tx.Exec(`
		UPDATE doc SET head_version = ?, title = ?, summary = ?, kind = ?,
		               source_path = ?, updated_at = ?, read_at = NULL, read_ratio = 0
		 WHERE id = ?`,
		res.VersionID, in.Title, nullIfEmpty(in.Summary), in.Kind,
		nullIfEmpty(in.SourcePath), now, docID); err != nil {
		return res, err
	}

	if in.RunID > 0 {
		if _, err := tx.Exec(`UPDATE doc SET run_id = ? WHERE id = ?`, in.RunID, docID); err != nil {
			return res, err
		}
	}
	if err := replacePushTags(tx, docID, in.Tags); err != nil {
		return res, err
	}
	if err := reindex(tx, docID, in.Title, in.Plain); err != nil {
		return res, err
	}

	res.Changed = true
	return res, tx.Commit()
}

// replacePushTags 只替换 source='push' 的标签，手动打的标签原样保留——
// 否则 agent 的下一次生成会把你标的「待复核」冲掉。
//
// 另一半是墓碑：用户手动删掉过的 front-matter 标签会留下 source='blocked'，
// 这里必须跳过，否则 agent 每重跑一次那个标签就复活一次。
func replacePushTags(tx *sql.Tx, docID int64, tags []string) error {
	blocked, err := blockedTagNames(tx, docID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_tag WHERE doc_id = ? AND source = 'push'`, docID); err != nil {
		return err
	}
	for _, name := range tags {
		name = strings.TrimSpace(name)
		if name == "" || blocked[name] {
			continue
		}
		tagID, err := ensureTag(tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO doc_tag (doc_id, tag_id, source) VALUES (?, ?, 'push')
			ON CONFLICT(doc_id, tag_id) DO NOTHING`, docID, tagID); err != nil {
			return err
		}
	}
	return nil
}

func blockedTagNames(tx *sql.Tx, docID int64) (map[string]bool, error) {
	rows, err := tx.Query(`
		SELECT t.name FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		 WHERE dt.doc_id = ? AND dt.source = 'blocked'`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// reindex 重建这篇文档的 FTS 行。rowid 就是 doc.id，且只索引 head 版本。
func reindex(tx *sql.Tx, docID int64, title, plain string) error {
	var tags string
	if err := tx.QueryRow(`
		SELECT COALESCE(GROUP_CONCAT(t.name, ' '), '')
		  FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		 WHERE dt.doc_id = ? AND dt.source <> 'blocked'`, docID).Scan(&tags); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_fts WHERE rowid = ?`, docID); err != nil {
		return err
	}
	_, err := tx.Exec(
		`INSERT INTO doc_fts (rowid, title, plain, tags) VALUES (?, ?, ?, ?)`,
		docID, title, plain, tags)
	return err
}

// ── 文档读取 ──────────────────────────────────────────────────────

func (s *Store) ListDocs(f DocFilter) ([]Doc, error) {
	q := docSelect
	args := []any{}
	where := []string{}

	if f.ProjectID > 0 {
		where = append(where, "d.project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.Unread {
		where = append(where, "d.read_at IS NULL")
	}
	if f.Later {
		where = append(where, "d.later = 1")
	}
	if f.Tag != "" {
		where = append(where, `d.id IN (SELECT dt.doc_id FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		                                 WHERE t.name = ? AND dt.source <> 'blocked')`)
		args = append(args, f.Tag)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY d.updated_at DESC, d.id DESC"

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q += " LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Doc{}
	ids := []int64{}
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
		ids = append(ids, d.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachTags(out, ids)
}

// attachTags 用一次查询给一批文档补上标签，避免 N+1。
func (s *Store) attachTags(docs []Doc, ids []int64) ([]Doc, error) {
	if len(ids) == 0 {
		return docs, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.Query(`
		SELECT dt.doc_id, t.name FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		 WHERE dt.source <> 'blocked' AND dt.doc_id IN (`+placeholders+`)
		 ORDER BY t.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[int64][]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		byID[id] = append(byID[id], name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range docs {
		if t := byID[docs[i].ID]; t != nil {
			docs[i].Tags = t
		}
	}
	return docs, nil
}

func (s *Store) GetDoc(id int64) (*DocDetail, error) {
	d, err := scanDoc(s.DB.QueryRow(docSelect+" WHERE d.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	detail := &DocDetail{Doc: d, Versions: []Version{}}
	err = s.DB.QueryRow(`
		SELECT v.html, v.toc FROM doc_version v
		  JOIN doc d ON d.head_version = v.id WHERE d.id = ?`, id).Scan(&detail.HTML, &detail.TOC)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if detail.TOC == "" {
		detail.TOC = "[]"
	}

	rows, err := s.DB.Query(
		`SELECT id, seq, chars, bytes, created_at FROM doc_version WHERE doc_id = ? ORDER BY seq DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.Seq, &v.Chars, &v.Bytes, &v.CreatedAt); err != nil {
			return nil, err
		}
		detail.Versions = append(detail.Versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	withTags, err := s.attachTags([]Doc{detail.Doc}, []int64{id})
	if err != nil {
		return nil, err
	}
	detail.Doc = withTags[0]

	// 批注随文档一起返回：阅读页需要它们才能画出高亮层，
	// 单独再发一个请求只会多一次往返和一次竞态。
	if detail.Annotations, err = s.ListDocAnnotations(id); err != nil {
		return nil, err
	}
	return detail, nil
}

// RawVersion 返回某个版本的原始内容 blob 与 MIME，
// 供 raw 模式的 iframe 直接取用。
func (s *Store) RawVersion(versionID int64) (sha, mimeType string, err error) {
	// 有内联过的版本就优先给它——iframe 里拿不到 Cookie，也可能根本没网。
	err = s.DB.QueryRow(`
		SELECT COALESCE(v.serve_blob, v.raw_blob), b.mime
		  FROM doc_version v
		  JOIN blob b ON b.sha = COALESCE(v.serve_blob, v.raw_blob)
		 WHERE v.id = ?`, versionID).Scan(&sha, &mimeType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return sha, mimeType, err
}

// MarkRead 更新阅读进度。ratio 达到阈值即认为读完。
func (s *Store) MarkRead(id int64, ratio float64, read bool) error {
	var readAt any
	if read {
		readAt = time.Now().Unix()
	}
	_, err := s.DB.Exec(
		`UPDATE doc SET read_ratio = MAX(read_ratio, ?), read_at = COALESCE(?, read_at) WHERE id = ?`,
		ratio, readAt, id)
	return err
}

func (s *Store) SetLater(id int64, later bool) error {
	_, err := s.DB.Exec(`UPDATE doc SET later = ? WHERE id = ?`, later, id)
	return err
}

func (s *Store) ListTags() ([]Tag, error) {
	rows, err := s.DB.Query(`
		SELECT t.id, t.name, COALESCE(t.color, ''), COUNT(dt.doc_id)
		  FROM tag t LEFT JOIN doc_tag dt ON dt.tag_id = t.id AND dt.source <> 'blocked'
		 GROUP BY t.id HAVING COUNT(dt.doc_id) > 0
		 ORDER BY COUNT(dt.doc_id) DESC, t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Stats 给顶栏用：总数与未读数。
func (s *Store) Stats() (total, unread int, err error) {
	err = s.DB.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN read_at IS NULL THEN 1 ELSE 0 END), 0) FROM doc`).
		Scan(&total, &unread)
	return
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
