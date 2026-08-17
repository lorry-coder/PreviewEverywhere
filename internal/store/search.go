package store

import (
	"html"
	"strings"

	"previeweverywhere/internal/search"
)

// SearchHit 是一条命中，附带一段带高亮的原文片段。
type SearchHit struct {
	Doc
	Snippet string `json:"snippet"`
}

// minTrigram 是 FTS5 trigram 分词器能索引的最短长度。
// 比它短的词（「双写」这种两字中文词）搜不到，得回落到 LIKE。
const minTrigram = 3

func (s *Store) Search(q search.Query, limit int) ([]SearchHit, error) {
	if q.Empty() {
		return []SearchHit{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}

	where := []string{}
	args := []any{}

	// 长词走 FTS5 索引，短词回落到 LIKE 全表扫。
	// 单用户万级文档的量级下，这条回落路径的代价可以忽略；
	// 真到十万级就该换成应用层分词（gse）而不是继续加 LIKE。
	var fts, likes []string
	for _, t := range q.Terms {
		if len([]rune(t)) >= minTrigram {
			fts = append(fts, t)
		} else {
			likes = append(likes, t)
		}
	}

	if expr := ftsExpr(fts); expr != "" {
		where = append(where, `d.id IN (SELECT rowid FROM doc_fts WHERE doc_fts MATCH ?)`)
		args = append(args, expr)
	}
	for _, t := range likes {
		pattern := "%" + escapeLike(t) + "%"
		where = append(where, `(v.plain LIKE ? ESCAPE '\' OR d.title LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}

	for _, tag := range q.Tags {
		where = append(where, `d.id IN (SELECT dt.doc_id FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		                                 WHERE t.name = ? AND dt.source <> 'blocked')`)
		args = append(args, tag)
	}
	for _, tag := range q.NotTags {
		where = append(where, `d.id NOT IN (SELECT dt.doc_id FROM doc_tag dt JOIN tag t ON t.id = dt.tag_id
		                                     WHERE t.name = ? AND dt.source <> 'blocked')`)
		args = append(args, tag)
	}
	if q.Project != "" {
		where = append(where, `(p.slug = ? OR p.name = ?)`)
		args = append(args, q.Project, q.Project)
	}
	if q.Kind != "" {
		where = append(where, `d.kind = ?`)
		args = append(args, q.Kind)
	}
	if q.Unread {
		where = append(where, `d.read_at IS NULL`)
	}
	if q.Read {
		where = append(where, `d.read_at IS NOT NULL`)
	}
	if q.Later {
		where = append(where, `d.later = 1`)
	}

	sql := `
		SELECT d.id, d.project_id, p.name, p.slug, d.source_key, COALESCE(d.source_path, ''),
		       d.title, COALESCE(d.summary, ''), d.kind, COALESCE(d.render_mode, ''),
		       COALESCE(v.seq, 0), COALESCE(v.chars, 0),
		       d.read_at IS NOT NULL, d.read_ratio, d.later,
		       d.created_at, d.updated_at, COALESCE(v.plain, '')
		  FROM doc d
		  JOIN project p ON p.id = d.project_id
		  LEFT JOIN doc_version v ON v.id = d.head_version`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}

	// 标题命中排在前面，其余按更新时间倒序。读 agent 产出时，
	// 「最近的」几乎总比「BM25 分最高的」更符合预期。
	if len(q.Terms) > 0 {
		sql += ` ORDER BY CASE WHEN d.title LIKE ? ESCAPE '\' THEN 0 ELSE 1 END, d.updated_at DESC, d.id DESC`
		args = append(args, "%"+escapeLike(q.Terms[0])+"%")
	} else {
		sql += ` ORDER BY d.updated_at DESC, d.id DESC`
	}
	sql += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := []SearchHit{}
	ids := []int64{}
	for rows.Next() {
		var h SearchHit
		var plain string
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.ProjectName, &h.ProjectSlug, &h.SourceKey,
			&h.SourcePath, &h.Title, &h.Summary, &h.Kind, &h.RenderMode, &h.Seq, &h.Chars,
			&h.Read, &h.ReadRatio, &h.Later, &h.CreatedAt, &h.UpdatedAt, &plain); err != nil {
			return nil, err
		}
		h.Tags = []string{}
		h.Snippet = Snippet(plain, q.Terms, 46)
		if h.Snippet == "" {
			h.Snippet = html.EscapeString(truncRunes(h.Summary, 110))
		}
		hits = append(hits, h)
		ids = append(ids, h.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 复用列表页的批量补标签，避免 N+1。
	docs := make([]Doc, len(hits))
	for i := range hits {
		docs[i] = hits[i].Doc
	}
	docs, err = s.attachTags(docs, ids)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		hits[i].Doc = docs[i]
	}
	return hits, nil
}

// ftsExpr 把关键词拼成 FTS5 表达式。每个词都用双引号包起来当短语，
// 内部的引号翻倍转义——否则用户输入一个引号就会让整条查询语法出错。
func ftsExpr(terms []string) string {
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " AND ")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Snippet 在正文里截一段包含关键词的上下文，并把命中处包上 <mark>。
//
// 自己实现而不是用 FTS5 的 snippet()，有两个理由：一是 LIKE 回落路径
// 拿不到 FTS 的片段，两条路径的结果得长得一样；二是窗口必须按字符而不是
// 字节来取，否则中文会被从半个字中间切开。
func Snippet(plain string, terms []string, radius int) string {
	if plain == "" || len(terms) == 0 {
		return ""
	}
	runes := []rune(plain)
	lower := []rune(strings.ToLower(plain))

	type span struct{ start, end int }
	var spans []span
	for _, t := range terms {
		needle := []rune(strings.ToLower(t))
		if len(needle) == 0 {
			continue
		}
		for at := 0; at+len(needle) <= len(lower); {
			if string(lower[at:at+len(needle)]) == string(needle) {
				spans = append(spans, span{at, at + len(needle)})
				at += len(needle)
			} else {
				at++
			}
		}
	}
	if len(spans) == 0 {
		return ""
	}

	first := spans[0]
	for _, s := range spans {
		if s.start < first.start {
			first = s
		}
	}
	start := max(0, first.start-radius)
	end := min(len(runes), first.end+radius)

	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	cursor := start
	for _, s := range spans {
		if s.start < cursor || s.end > end {
			continue
		}
		b.WriteString(html.EscapeString(string(runes[cursor:s.start])))
		b.WriteString("<mark>")
		b.WriteString(html.EscapeString(string(runes[s.start:s.end])))
		b.WriteString("</mark>")
		cursor = s.end
	}
	b.WriteString(html.EscapeString(string(runes[cursor:end])))
	if end < len(runes) {
		b.WriteString("…")
	}
	// 正文里的段落分隔在片段里没有意义，压成空格。
	return strings.Join(strings.Fields(b.String()), " ")
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
