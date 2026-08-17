package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Sha256Hex 返回内容的 sha256 十六进制串。管线的去重与 blob 命名都用它。
func Sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// blobPath 把 sha 拆成两级目录，避免单目录下堆积几十万个文件。
func (s *Store) blobPath(sha string) string {
	return filepath.Join(s.dataDir, "blobs", sha[0:2], sha[2:4], sha)
}

// PutBlob 内容寻址地存一份数据；同样的内容重复写入不产生额外开销。
func (s *Store) PutBlob(data []byte, mime string) (string, error) {
	sha := Sha256Hex(data)
	path := s.blobPath(sha)

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("创建 blob 目录失败: %w", err)
		}
		// 先写临时文件再改名，避免进程中断留下半个文件被后续当成完整内容。
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return "", fmt.Errorf("写入 blob 失败: %w", err)
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("提交 blob 失败: %w", err)
		}
	} else if err != nil {
		return "", err
	}

	_, err := s.DB.Exec(
		`INSERT INTO blob (sha, mime, bytes, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(sha) DO NOTHING`,
		sha, mime, len(data), time.Now().Unix())
	if err != nil {
		return "", fmt.Errorf("登记 blob 失败: %w", err)
	}
	return sha, nil
}

// BlobMeta 返回 blob 的 MIME 与磁盘路径，供 HTTP 层直接 ServeFile。
func (s *Store) BlobMeta(sha string) (mime, path string, err error) {
	err = s.DB.QueryRow(`SELECT mime FROM blob WHERE sha = ?`, sha).Scan(&mime)
	if err != nil {
		return "", "", err
	}
	return mime, s.blobPath(sha), nil
}

// ReadBlob 读回原始内容。
func (s *Store) ReadBlob(sha string) ([]byte, error) {
	return os.ReadFile(s.blobPath(sha))
}
