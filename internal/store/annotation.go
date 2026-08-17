package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"previeweverywhere/internal/anchor"
)

// Annotation 是一条批注。它挂在 doc 上而不是 doc_version 上——
// 批注要跨版本存活，这是整个 P3 的前提。
type Annotation struct {
	ID          int64  `json:"id"`
	DocID       int64  `json:"docId"`
	DocTitle    string `json:"docTitle,omitempty"`
	ProjectName string `json:"projectName,omitempty"`

	Kind  string `json:"kind"` // highlight | note | todo | question
	Color string `json:"color,omitempty"`
	Body  string `json:"body,omitempty"`

	Blk      string `json:"blk"`
	StartOff int    `json:"startOff"`
	EndOff   int    `json:"endOff"`
	Quote    string `json:"quote"`

	State      string `json:"state"` // ok | moved | orphan
	OrphanNote string `json:"orphanNote,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// AnnotationKinds 是允许的四种类型。
//
// 不只是「高亮」和「笔记」：对读 agent 产出这件事来说，
// todo（要去改的）和 question（要去确认的）是两个高频且性质不同的动作，
// 它们会汇总成一张全局列表，那张列表本身就能直接喂给下一轮 agent。
var AnnotationKinds = map[string]bool{
	"highlight": true, "note": true, "todo": true, "question": true,
}

// NewAnnotation 是创建一条批注所需的输入。
//
// 前端只负责给出「哪个块 + 块内起止偏移 + 它以为自己选中的文字」，
// 引文与前后文一律由服务端从存储的纯文本里取——只有这样，
// 日后重定位时搜索的内容才和当初存下的严格一致。
type NewAnnotation struct {
	DocID    int64
	Kind     string
	Color    string
	Body     string
	Blk      string
	StartOff int
	EndOff   int
	Exact    string // 前端的自述，用于校正偏移
}

const quoteMax = 400

func (s *Store) CreateAnnotation(in NewAnnotation) (*Annotation, error) {
	if !AnnotationKinds[in.Kind] {
		return nil, fmt.Errorf("未知的批注类型: %s", in.Kind)
	}

	versionID, plain, blocks, err := s.headContent(in.DocID)
	if err != nil {
		return nil, err
	}

	a, err := resolveNewAnchor(plain, blocks, in)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	res, err := s.DB.Exec(`
		INSERT INTO annotation
		  (doc_id, kind, color, body, blk, start_off, end_off,
		   quote_prefix, quote_exact, quote_suffix, doc_off,
		   state, born_version, bound_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ok', ?, ?, ?, ?)`,
		in.DocID, in.Kind, nullIfEmpty(in.Color), nullIfEmpty(in.Body),
		a.Blk, a.StartOff, a.EndOff, a.Prefix, a.Exact, a.Suffix, a.DocOff,
		versionID, versionID, now, now)
	if err != nil {
		return nil, fmt.Errorf("写入批注失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetAnnotation(id)
}

// resolveNewAnchor 把前端给的偏移落实到服务端的纯文本上。
//
// 前端的偏移是在浏览器 DOM 上算出来的，服务端的偏移来自渲染时的规范化文本。
// 两边的规范化规则是照着写的，但只要有一点点漂移就会让批注错位，
// 所以这里拿前端自述的引文校正一次：对得上就用，对不上就在块内找回来。
func resolveNewAnchor(plain string, blocks []anchor.Block, in NewAnnotation) (anchor.Anchor, error) {
	blockText, ok := anchor.BlockText(plain, blocks, in.Blk)
	if !ok {
		return anchor.Anchor{}, fmt.Errorf("文档里没有块 %s，可能文档已经更新过了", in.Blk)
	}
	br := []rune(blockText)

	start, end := clamp(in.StartOff, 0, len(br)), clamp(in.EndOff, 0, len(br))
	if start > end {
		start, end = end, start
	}

	want := strings.TrimSpace(in.Exact)
	if want != "" && string(br[start:end]) != want {
		if at := indexRunes(br, []rune(want)); at >= 0 {
			start, end = at, at+len([]rune(want))
		}
	}
	if start == end {
		return anchor.Anchor{}, errors.New("选区是空的")
	}

	var blockOff int
	for _, b := range blocks {
		if b.Blk == in.Blk {
			blockOff = b.Off
		}
	}
	text := []rune(plain)
	docOff := blockOff + start

	return anchor.Anchor{
		Blk:      in.Blk,
		StartOff: start,
		EndOff:   end,
		Exact:    anchor.TrimQuote(string(br[start:end]), quoteMax),
		Prefix:   contextBefore(text, docOff),
		Suffix:   contextAfter(text, docOff+(end-start)),
		DocOff:   docOff,
	}, nil
}

// ── 重定位 ────────────────────────────────────────────────────────

// RelocateAnnotations 在文档产生新版本后，把它的全部批注重新定位一遍。
//
// 刻意放在写入侧而不是读取侧：手机打开文档时批注已经就位，不用在弱设备上
// 做模糊匹配；而且同一份计算所有设备共享，结果永远一致。
func (s *Store) RelocateAnnotations(docID, versionID int64) (ok, moved, orphan int, err error) {
	plain, blocks, err := s.versionContent(versionID)
	if err != nil {
		return 0, 0, 0, err
	}
	var seq int
	s.DB.QueryRow(`SELECT seq FROM doc_version WHERE id = ?`, versionID).Scan(&seq)

	rows, err := s.DB.Query(`
		SELECT id, blk, start_off, end_off, quote_prefix, quote_exact, quote_suffix, doc_off
		  FROM annotation WHERE doc_id = ?`, docID)
	if err != nil {
		return 0, 0, 0, err
	}
	type pending struct {
		id int64
		a  anchor.Anchor
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.a.Blk, &p.a.StartOff, &p.a.EndOff,
			&p.a.Prefix, &p.a.Exact, &p.a.Suffix, &p.a.DocOff); err != nil {
			rows.Close()
			return 0, 0, 0, err
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, 0, err
	}
	if len(list) == 0 {
		return 0, 0, 0, nil
	}

	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()

	for _, p := range list {
		r := anchor.Relocate(p.a, plain, blocks)
		switch r.State {
		case anchor.StateOK:
			ok++
		case anchor.StateMoved:
			moved++
		default:
			orphan++
		}

		var boundVersion any
		var note any
		if r.State == anchor.StateOrphan {
			// 不删，留快照。原文真的消失时任何算法都只能猜，
			// 与其猜错不如把判断权交回给人。
			note = fmt.Sprintf("原文在 v%d 中消失", seq)
		} else {
			boundVersion = versionID
		}

		if _, err := tx.Exec(`
			UPDATE annotation SET blk = ?, start_off = ?, end_off = ?,
			       quote_prefix = ?, quote_exact = ?, quote_suffix = ?, doc_off = ?,
			       state = ?, bound_version = ?, orphan_note = ?, updated_at = ?
			 WHERE id = ?`,
			r.Anchor.Blk, r.Anchor.StartOff, r.Anchor.EndOff,
			r.Anchor.Prefix, r.Anchor.Exact, r.Anchor.Suffix, r.Anchor.DocOff,
			string(r.State), boundVersion, note, now, p.id); err != nil {
			return 0, 0, 0, err
		}
	}
	return ok, moved, orphan, tx.Commit()
}

// Rebind 把一条失联批注手动重挂到新的选区上。
func (s *Store) Rebind(annID int64, blk string, startOff, endOff int, exact string) (*Annotation, error) {
	var docID int64
	if err := s.DB.QueryRow(`SELECT doc_id FROM annotation WHERE id = ?`, annID).Scan(&docID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	versionID, plain, blocks, err := s.headContent(docID)
	if err != nil {
		return nil, err
	}
	a, err := resolveNewAnchor(plain, blocks, NewAnnotation{
		DocID: docID, Blk: blk, StartOff: startOff, EndOff: endOff, Exact: exact,
	})
	if err != nil {
		return nil, err
	}
	_, err = s.DB.Exec(`
		UPDATE annotation SET blk = ?, start_off = ?, end_off = ?,
		       quote_prefix = ?, quote_exact = ?, quote_suffix = ?, doc_off = ?,
		       state = 'ok', bound_version = ?, orphan_note = NULL, updated_at = ?
		 WHERE id = ?`,
		a.Blk, a.StartOff, a.EndOff, a.Prefix, a.Exact, a.Suffix, a.DocOff,
		versionID, time.Now().Unix(), annID)
	if err != nil {
		return nil, err
	}
	return s.GetAnnotation(annID)
}

// ── 读取与编辑 ────────────────────────────────────────────────────

const annSelect = `
	SELECT a.id, a.doc_id, d.title, p.name, a.kind, COALESCE(a.color, ''),
	       COALESCE(a.body, ''), a.blk, a.start_off, a.end_off, a.quote_exact,
	       a.state, COALESCE(a.orphan_note, ''), a.created_at, a.updated_at
	  FROM annotation a
	  JOIN doc d ON d.id = a.doc_id
	  JOIN project p ON p.id = d.project_id`

func scanAnnotations(rows *sql.Rows) ([]Annotation, error) {
	out := []Annotation{}
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.DocID, &a.DocTitle, &a.ProjectName, &a.Kind,
			&a.Color, &a.Body, &a.Blk, &a.StartOff, &a.EndOff, &a.Quote,
			&a.State, &a.OrphanNote, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAnnotation(id int64) (*Annotation, error) {
	rows, err := s.DB.Query(annSelect+" WHERE a.id = ?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := scanAnnotations(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *Store) ListDocAnnotations(docID int64) ([]Annotation, error) {
	rows, err := s.DB.Query(annSelect+
		" WHERE a.doc_id = ? ORDER BY a.state = 'orphan', a.doc_off", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnotations(rows)
}

// ListActionable 汇总所有待办与疑问。
//
// 这张列表是「在通勤路上读文档」这件事的产出物：你散落在十几份文档里的
// 待办和疑问，回到电脑前是一张清单，而且可以直接喂给下一轮 agent。
func (s *Store) ListActionable(kinds []string) ([]Annotation, error) {
	if len(kinds) == 0 {
		kinds = []string{"todo", "question"}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	args := make([]any, len(kinds))
	for i, k := range kinds {
		args[i] = k
	}
	rows, err := s.DB.Query(annSelect+
		" WHERE a.kind IN ("+placeholders+") ORDER BY a.created_at DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnotations(rows)
}

func (s *Store) UpdateAnnotation(id int64, kind, color, body *string) (*Annotation, error) {
	sets := []string{"updated_at = ?"}
	args := []any{time.Now().Unix()}
	if kind != nil {
		if !AnnotationKinds[*kind] {
			return nil, fmt.Errorf("未知的批注类型: %s", *kind)
		}
		sets = append(sets, "kind = ?")
		args = append(args, *kind)
	}
	if color != nil {
		sets = append(sets, "color = ?")
		args = append(args, nullIfEmpty(*color))
	}
	if body != nil {
		sets = append(sets, "body = ?")
		args = append(args, nullIfEmpty(*body))
	}
	args = append(args, id)

	if _, err := s.DB.Exec("UPDATE annotation SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return nil, err
	}
	return s.GetAnnotation(id)
}

func (s *Store) DeleteAnnotation(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM annotation WHERE id = ?`, id)
	return err
}

// AnnotationCounts 给列表页用：每篇文档有几条批注、几条失联。
func (s *Store) AnnotationCounts() (map[int64][2]int, error) {
	rows, err := s.DB.Query(`
		SELECT doc_id, COUNT(*), SUM(CASE WHEN state = 'orphan' THEN 1 ELSE 0 END)
		  FROM annotation GROUP BY doc_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][2]int{}
	for rows.Next() {
		var id int64
		var total, orphan int
		if err := rows.Scan(&id, &total, &orphan); err != nil {
			return nil, err
		}
		out[id] = [2]int{total, orphan}
	}
	return out, rows.Err()
}

// ── 版本内容 ──────────────────────────────────────────────────────

func (s *Store) headContent(docID int64) (versionID int64, plain string, blocks []anchor.Block, err error) {
	var raw string
	err = s.DB.QueryRow(`
		SELECT v.id, v.plain, v.blocks FROM doc d
		  JOIN doc_version v ON v.id = d.head_version WHERE d.id = ?`, docID).
		Scan(&versionID, &plain, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil, ErrNotFound
	}
	if err != nil {
		return 0, "", nil, err
	}
	blocks, err = decodeBlocks(raw)
	return versionID, plain, blocks, err
}

func (s *Store) versionContent(versionID int64) (plain string, blocks []anchor.Block, err error) {
	var raw string
	err = s.DB.QueryRow(`SELECT plain, blocks FROM doc_version WHERE id = ?`, versionID).Scan(&plain, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	blocks, err = decodeBlocks(raw)
	return plain, blocks, err
}

func decodeBlocks(raw string) ([]anchor.Block, error) {
	if strings.TrimSpace(raw) == "" {
		return []anchor.Block{}, nil
	}
	var out []anchor.Block
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("块索引解析失败: %w", err)
	}
	return out, nil
}

// ── 小工具 ────────────────────────────────────────────────────────

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func indexRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}

func contextBefore(text []rune, at int) string {
	lo := at - anchor.ContextLen
	if lo < 0 {
		lo = 0
	}
	if lo > at || at > len(text) {
		return ""
	}
	return string(text[lo:at])
}

func contextAfter(text []rune, at int) string {
	if at > len(text) {
		return ""
	}
	hi := at + anchor.ContextLen
	if hi > len(text) {
		hi = len(text)
	}
	return string(text[at:hi])
}

// DebugHeadBlocks 暴露当前版本的块索引与纯文本。
// 测试与「手动重挂」都需要在服务端侧定位引文，这是唯一的读取口。
func (s *Store) DebugHeadBlocks(docID int64) ([]anchor.Block, string, error) {
	_, plain, blocks, err := s.headContent(docID)
	return blocks, plain, err
}
