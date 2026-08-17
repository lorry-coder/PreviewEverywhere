package store

import "time"

// TimelineGroup 是首页时间线里的一组文档。
//
// 有 agent 会话 ID（run）时按会话分组——你想看的往往是「昨晚那次跑出了什么」。
// 没有 run 时（P5 的 Claude Code hook 落地之前，文件监听收进来的文档都属于
// 这种情况）降级成「同一天 + 同一项目」，保证时间线在任何时候都有意义。
type TimelineGroup struct {
	Key         string `json:"key"`
	RunID       int64  `json:"runId,omitempty"`
	RunLabel    string `json:"runLabel,omitempty"`
	ProjectID   int64  `json:"projectId"`
	ProjectName string `json:"projectName"`
	At          int64  `json:"at"`     // 组内最新的更新时间
	Unread      int    `json:"unread"` // 未读篇数
	Docs        []Doc  `json:"docs"`
}

func (s *Store) Timeline(limit int) ([]TimelineGroup, error) {
	if limit <= 0 || limit > 400 {
		limit = 150
	}

	rows, err := s.DB.Query(`
		SELECT d.id, d.project_id, p.name, p.slug, d.source_key, COALESCE(d.source_path, ''),
		       d.title, COALESCE(d.summary, ''), d.kind, COALESCE(d.render_mode, ''),
		       COALESCE(v.seq, 0), COALESCE(v.chars, 0),
		       d.read_at IS NOT NULL, d.read_ratio, d.later,
		       d.created_at, d.updated_at,
		       COALESCE(d.run_id, 0), COALESCE(r.label, ''), COALESCE(r.external_id, '')
		  FROM doc d
		  JOIN project p ON p.id = d.project_id
		  LEFT JOIN doc_version v ON v.id = d.head_version
		  LEFT JOIN run r ON r.id = d.run_id
		 ORDER BY d.updated_at DESC, d.id DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []TimelineGroup{}
	index := map[string]int{}
	docs := []Doc{}
	ids := []int64{}
	// 组内文档在 groups 里的位置，等标签补完再回填。
	positions := [][2]int{}

	for rows.Next() {
		var d Doc
		var runID int64
		var runLabel, runExternal string
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ProjectName, &d.ProjectSlug, &d.SourceKey,
			&d.SourcePath, &d.Title, &d.Summary, &d.Kind, &d.RenderMode, &d.Seq, &d.Chars,
			&d.Read, &d.ReadRatio, &d.Later, &d.CreatedAt, &d.UpdatedAt,
			&runID, &runLabel, &runExternal); err != nil {
			return nil, err
		}
		d.Tags = []string{}

		key := groupKey(runID, d.ProjectID, d.UpdatedAt, time.Local)
		gi, ok := index[key]
		if !ok {
			label := runLabel
			if label == "" && runExternal != "" {
				label = shortRunID(runExternal)
			}
			groups = append(groups, TimelineGroup{
				Key:         key,
				RunID:       runID,
				RunLabel:    label,
				ProjectID:   d.ProjectID,
				ProjectName: d.ProjectName,
				At:          d.UpdatedAt,
				Docs:        []Doc{},
			})
			gi = len(groups) - 1
			index[key] = gi
		}
		if !d.Read {
			groups[gi].Unread++
		}
		groups[gi].Docs = append(groups[gi].Docs, d)

		positions = append(positions, [2]int{gi, len(groups[gi].Docs) - 1})
		docs = append(docs, d)
		ids = append(ids, d.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	docs, err = s.attachTags(docs, ids)
	if err != nil {
		return nil, err
	}
	for i, pos := range positions {
		groups[pos[0]].Docs[pos[1]] = docs[i]
	}
	return groups, nil
}

// groupKey 决定一篇文档落进时间线的哪一组。
//
// 时区是显式参数而不是直接用 time.Local，因为这里对时区敏感得超出直觉：
// 分组按「服务端本地日期」算，而界面上的「今天 / 昨天」是按浏览器时区显示的。
// 两边不一致时，半夜前后写的文档会被归到昨天却标着「今天」。
// 部署在容器里尤其容易踩——容器默认 UTC。
func groupKey(runID, projectID, updatedAt int64, loc *time.Location) string {
	if runID > 0 {
		return "run:" + itoa64(runID)
	}
	day := time.Unix(updatedAt, 0).In(loc).Format("2006-01-02")
	return "day:" + day + ":p" + itoa64(projectID)
}

// shortRunID 把 session_id 这类长标识截短，纯粹是为了在界面上能看。
func shortRunID(external string) string {
	r := []rune(external)
	if len(r) <= 8 {
		return external
	}
	return string(r[:8])
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
