package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteParityFixtures 把规范化的输入输出导出成 JSON，
// 供前端的一致性检查（scripts/parity.sh）比对。
//
// 前端算偏移用的是它自己那份 normalize，服务端存的偏移用的是这一份。
// 两边一旦漂移，批注就会整体错位，而且错得很隐蔽——所以要有东西盯着。
func TestWriteParityFixtures(t *testing.T) {
	inputs := []string{
		"简单一句话",
		"一段话，\n中间换了行。",
		"English words   with   extra spaces",
		"中文和 English 混排",
		"English 后面接中文",
		"行首有空格   ",
		"   开头也有",
		"制表\t符\t分隔",
		"零宽\u200b字符\u200c混入\ufeff其中",
		"多个\n\n\n换行",
		"中文。\nEnglish start",
		"日本語のテキスト\nと改行",
		"한글 텍스트\n줄바꿈",
		"全角（括号）与半角(parens)",
		"emoji 😀 和中文",
		"",
		"   ",
		"a b",
	}

	out := make([]map[string]string, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, map[string]string{"in": in, "want": normalizeSpace(in)})
	}

	path := filepath.Join("..", "..", "web", "parity-fixtures.json")
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("写 fixture 失败: %v", err)
	}
}
