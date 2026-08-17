package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"previeweverywhere/internal/ingest"
)

// 跨机推送时，服务端看不到你的文件系统，文档里 ![](./img/a.png) 这种
// 相对引用就成了一张无声的坏图。所以把被引用的图片一起打包发过去。
//
// 刻意不改写正文：正文原样送达，图片作为附件另发，服务端在渲染时
// 用它们替换引用。这样平台里存的「原文」仍和你磁盘上的那份逐字节相同。

var (
	mdImageRef   = regexp.MustCompile(`!\[[^\]]*\]\(\s*([^)\s]+)`)
	htmlImageRef = regexp.MustCompile(`(?i)<img[^>]+src\s*=\s*["']([^"']+)["']`)
)

const maxPushAssetBytes = 2 << 20

// collectLocalAssets 找出文档里引用的本地图片并读进来，键是文档里写的原始引用。
// 读不到、太大、越界的一律跳过——跳过的后果只是那张图不显示，
// 不该让整篇文档推不上去。
func collectLocalAssets(content []byte, docPath string) map[string][]byte {
	if docPath == "" {
		return nil
	}
	baseDir := filepath.Dir(docPath)
	root := ingest.DetectProject(docPath).Root
	if root == "" {
		root = baseDir
	}

	out := map[string][]byte{}
	text := string(content)
	for _, re := range []*regexp.Regexp{mdImageRef, htmlImageRef} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			ref := m[1]
			if _, seen := out[ref]; seen {
				continue
			}
			data, ok := readLocalAsset(ref, baseDir, root)
			if ok {
				out[ref] = data
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readLocalAsset(ref, baseDir, root string) ([]byte, bool) {
	clean := ref
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	if clean == "" || strings.HasPrefix(clean, "data:") ||
		strings.HasPrefix(clean, "http://") || strings.HasPrefix(clean, "https://") ||
		strings.HasPrefix(clean, "//") || strings.HasPrefix(clean, "/") {
		return nil, false
	}
	if !ingest.IsAssetExt(path.Ext(clean)) {
		return nil, false
	}

	abs := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(clean)))
	// 文档内容是不可信输入，越界引用一律拒绝——和服务端同机时的规则一致。
	rel, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false
	}

	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() > maxPushAssetBytes {
		return nil, false
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false
	}
	return data, true
}
