package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 问题反馈的三种状态。用英文存、中文显示：存进库里的东西要稳定，
// 而给人看的字随时可能改措辞。
const (
	FeedbackOpen    = "open"    // 待修复
	FeedbackFixed   = "fixed"   // 已修复
	FeedbackWontFix = "wontfix" // 无需修复
)

// FeedbackStatusLabel 是给人看的名字，CLI 与界面共用一套措辞。
var FeedbackStatusLabel = map[string]string{
	FeedbackOpen:    "待修复",
	FeedbackFixed:   "已修复",
	FeedbackWontFix: "无需修复",
}

func ValidFeedbackStatus(s string) bool {
	_, ok := FeedbackStatusLabel[s]
	return ok
}

type Feedback struct {
	ID         int64  `json:"id"`
	Body       string `json:"body"`
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
	DocID      int64  `json:"docId,omitempty"`
	DocTitle   string `json:"docTitle,omitempty"`
	Route      string `json:"route,omitempty"`
	// Env 是提交时的环境快照，原样透传的 JSON 字符串。
	Env       string `json:"env,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type NewFeedback struct {
	Body     string
	DocID    int64
	DocTitle string
	Route    string
	Env      string
}

// AddFeedback 记一条反馈。
func (s *Store) AddFeedback(in NewFeedback) (*Feedback, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, errors.New("反馈内容不能为空")
	}
	now := time.Now().Unix()
	res, err := s.DB.Exec(`
		INSERT INTO feedback (body, status, doc_id, doc_title, route, env, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		body, FeedbackOpen, nullIfZero(in.DocID), nullIfEmpty(in.DocTitle),
		nullIfEmpty(in.Route), nullIfEmpty(in.Env), now, now)
	if err != nil {
		return nil, fmt.Errorf("保存反馈失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.Feedback(id)
}

// ListFeedback 按状态列出反馈。status 为空表示不限。
func (s *Store) ListFeedback(status string) ([]Feedback, error) {
	q := `SELECT id, body, status, COALESCE(resolution,''), COALESCE(doc_id,0),
	             COALESCE(doc_title,''), COALESCE(route,''), COALESCE(env,''),
	             created_at, updated_at
	        FROM feedback`
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	// 待修复的排在最前：这张表存在的意义就是「还有什么没处理」。
	q += ` ORDER BY CASE status WHEN 'open' THEN 0 ELSE 1 END, created_at DESC`

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Feedback{}
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.Body, &f.Status, &f.Resolution, &f.DocID,
			&f.DocTitle, &f.Route, &f.Env, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Feedback(id int64) (*Feedback, error) {
	var f Feedback
	err := s.DB.QueryRow(`
		SELECT id, body, status, COALESCE(resolution,''), COALESCE(doc_id,0),
		       COALESCE(doc_title,''), COALESCE(route,''), COALESCE(env,''),
		       created_at, updated_at
		  FROM feedback WHERE id = ?`, id).
		Scan(&f.ID, &f.Body, &f.Status, &f.Resolution, &f.DocID,
			&f.DocTitle, &f.Route, &f.Env, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// SetFeedbackStatus 改状态，可同时写一句处理说明。
func (s *Store) SetFeedbackStatus(id int64, status, resolution string) (*Feedback, error) {
	if !ValidFeedbackStatus(status) {
		return nil, fmt.Errorf("未知状态 %q，可用: open / fixed / wontfix", status)
	}
	res, err := s.DB.Exec(`
		UPDATE feedback SET status = ?, resolution = ?, updated_at = ? WHERE id = ?`,
		status, nullIfEmpty(strings.TrimSpace(resolution)), time.Now().Unix(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Feedback(id)
}

func (s *Store) DeleteFeedback(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM feedback WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FeedbackCounts 各状态各有多少条，用于界面上的分组标题。
func (s *Store) FeedbackCounts() (map[string]int, error) {
	rows, err := s.DB.Query(`SELECT status, count(*) FROM feedback GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
