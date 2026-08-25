package store

import "fmt"

// AssetRefs 取某个版本的「引用 → blob」映射，按出现次序排列。
func (s *Store) AssetRefs(versionID int64) ([]AssetRef, error) {
	rows, err := s.DB.Query(
		`SELECT ord, ref, sha FROM asset_ref WHERE version_id = ? ORDER BY ord`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetRef{}
	for rows.Next() {
		var a AssetRef
		if err := rows.Scan(&a.Ord, &a.Ref, &a.Sha); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// HasAssetRefs 这个版本有没有记录过映射。
//
// 用来区分两种「没有图片」：真的一张图都没有，还是入库时还没有这张表。
// 前者可以放心导出，后者要走顺序配对的兜底路径。
func (s *Store) HasAssetRefs(versionID int64) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT count(*) FROM asset_ref WHERE version_id = ?`, versionID).Scan(&n)
	return n > 0, err
}

// VersionRaw 取某个版本的原文与它的 blob 信息。
func (s *Store) VersionRaw(versionID int64) (raw []byte, mime string, err error) {
	sha, mimeType, err := s.RawVersion(versionID)
	if err != nil {
		return nil, "", err
	}
	data, err := s.ReadBlob(sha)
	if err != nil {
		return nil, "", fmt.Errorf("原文丢失: %w", err)
	}
	return data, mimeType, nil
}

// VersionHTML 取某个版本渲染后的 HTML。
func (s *Store) VersionHTML(versionID int64) (string, error) {
	var html string
	err := s.DB.QueryRow(`SELECT html FROM doc_version WHERE id = ?`, versionID).Scan(&html)
	return html, err
}
