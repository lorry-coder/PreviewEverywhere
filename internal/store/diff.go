package store

import (
	"database/sql"
	"errors"

	"previeweverywhere/internal/anchor"
)

// VersionDiff 是两个版本之间的块级差异。
//
// 这是 09 章里投入产出比最高的功能：agent 重跑一次，旧办法是从头再读一遍；
// 有了它，界面上直接告诉你「新增 3 段、修改 1 段」，再给一个「只看变化」开关，
// 一篇 3000 字报告的重读成本从 10 分钟压到 30 秒。
// 存储成本几乎为零——版本本来就存着，块 ID 本来就是内容哈希。
type VersionDiff struct {
	FromSeq int `json:"fromSeq"`
	ToSeq   int `json:"toSeq"`

	// Changed 是新版本里「新增或被改写」的块 ID。
	// 块 ID 就是内容哈希，所以「改写」和「新增」在这一层是同一回事，
	// 界面上也不需要区分——你要读的都是这些。
	Changed []string `json:"changed"`
	Removed int      `json:"removed"`
	Same    int      `json:"same"`
}

func (s *Store) DiffVersions(docID int64, fromSeq, toSeq int) (*VersionDiff, error) {
	from, fromBlocks, err := s.blocksAtSeq(docID, fromSeq)
	if err != nil {
		return nil, err
	}
	to, toBlocks, err := s.blocksAtSeq(docID, toSeq)
	if err != nil {
		return nil, err
	}

	oldIDs := blockIDs(fromBlocks)
	newIDs := blockIDs(toBlocks)
	keptInNew := commonSubsequence(oldIDs, newIDs)

	d := &VersionDiff{FromSeq: from, ToSeq: to, Changed: []string{}}
	for i, id := range newIDs {
		if keptInNew[i] {
			d.Same++
		} else {
			d.Changed = append(d.Changed, id)
		}
	}
	d.Removed = len(oldIDs) - d.Same
	return d, nil
}

func (s *Store) blocksAtSeq(docID int64, seq int) (int, []anchor.Block, error) {
	var raw string
	var actual int
	query := `SELECT seq, blocks FROM doc_version WHERE doc_id = ? AND seq = ?`
	args := []any{docID, seq}
	if seq <= 0 {
		// seq<=0 表示「当前版本」，省得调用方自己去查。
		query = `SELECT seq, blocks FROM doc_version WHERE doc_id = ? ORDER BY seq DESC LIMIT 1`
		args = []any{docID}
	}
	err := s.DB.QueryRow(query, args...).Scan(&actual, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, ErrNotFound
	}
	if err != nil {
		return 0, nil, err
	}
	blocks, err := decodeBlocks(raw)
	return actual, blocks, err
}

func blockIDs(blocks []anchor.Block) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = b.Blk
	}
	return out
}

// commonSubsequence 求最长公共子序列，返回 b 中哪些位置属于公共部分。
//
// 用 LCS 而不是集合比较，是为了让「整段挪了位置」不被误报成
// 「删了一段又加了一段」——顺序信息在这里是有意义的。
func commonSubsequence(a, b []string) []bool {
	n, m := len(a), len(b)
	kept := make([]bool, m)
	if n == 0 || m == 0 {
		return kept
	}

	// dp[i][j] = a[i:] 与 b[j:] 的 LCS 长度
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			kept[j] = true
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return kept
}
