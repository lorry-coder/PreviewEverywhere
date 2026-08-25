package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeedbackLifecycle(t *testing.T) {
	s := testStore(t)

	f, err := s.AddFeedback(NewFeedback{
		Body:  "长按之后气泡出来了，但是没有选中标记",
		Route: "#/doc/1",
		Env:   `{"build":"index-abc.js","viewport":"390×844","pointer":"coarse"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != FeedbackOpen {
		t.Errorf("新反馈应当是待修复，实得 %s", f.Status)
	}

	// 空内容不该被接受——一条没有描述的反馈对以后的自己毫无用处
	if _, err := s.AddFeedback(NewFeedback{Body: "   "}); err == nil {
		t.Error("空反馈应当被拒绝")
	}

	if _, err := s.SetFeedbackStatus(f.ID, FeedbackFixed, "改成三值判定后修好了"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Feedback(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != FeedbackFixed || got.Resolution == "" {
		t.Errorf("状态或处理说明没写进去: %+v", got)
	}

	if _, err := s.SetFeedbackStatus(f.ID, "随便写的", ""); err == nil {
		t.Error("未知状态应当被拒绝")
	}
	if _, err := s.SetFeedbackStatus(99999, FeedbackFixed, ""); err != ErrNotFound {
		t.Error("改不存在的反馈应当返回 ErrNotFound")
	}

	if err := s.DeleteFeedback(f.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFeedback(f.ID); err != ErrNotFound {
		t.Error("重复删除应当返回 ErrNotFound")
	}
}

// 待修复的必须排在最前：这张表存在的意义就是「还有什么没处理」。
func TestFeedbackOpenFirst(t *testing.T) {
	s := testStore(t)
	a, _ := s.AddFeedback(NewFeedback{Body: "先提的，后来修好了"})
	s.SetFeedbackStatus(a.ID, FeedbackFixed, "")
	s.AddFeedback(NewFeedback{Body: "后提的，还没修"})

	list, err := s.ListFeedback("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应当有 2 条，实得 %d", len(list))
	}
	if list[0].Status != FeedbackOpen {
		t.Errorf("待修复的应当排最前，实得 %s", list[0].Status)
	}

	only, _ := s.ListFeedback(FeedbackFixed)
	if len(only) != 1 || only[0].Status != FeedbackFixed {
		t.Errorf("按状态过滤失效: %+v", only)
	}
}

// 文档被删掉之后，挂在它上面的反馈必须还在——否则「删掉出问题的文档」
// 会顺手把问题记录也抹掉，正好是最不该丢的时候丢。
func TestFeedbackSurvivesDocDeletion(t *testing.T) {
	s := testStore(t)
	docID := seedDoc(t, s, "甲.md", "正文")
	f, err := s.AddFeedback(NewFeedback{Body: "这篇渲染不对", DocID: docID, DocTitle: "甲.md"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDoc(docID, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Feedback(f.ID)
	if err != nil {
		t.Fatalf("文档删了之后反馈也没了: %v", err)
	}
	if got.DocID != 0 {
		t.Errorf("doc_id 应当被置空，实得 %d", got.DocID)
	}
	if got.DocTitle != "甲.md" {
		t.Errorf("标题快照应当留着，实得 %q", got.DocTitle)
	}
}

// feedback.md 是投影：只写不读，全量重写，所以不可能和库漂移。
func TestFeedbackFileIsFullRewrite(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()

	a, _ := s.AddFeedback(NewFeedback{Body: "第一个问题"})
	s.AddFeedback(NewFeedback{Body: "第二个问题"})
	if err := s.WriteFeedbackFile(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FeedbackFileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"第一个问题", "第二个问题", "待修复（2）", "已修复（0）", "改这个文件不会生效"} {
		if !strings.Contains(text, want) {
			t.Errorf("生成的文件里缺少 %q", want)
		}
	}

	// 删掉一条再重写：旧内容必须消失，这正是「全量重写」的意义
	if err := s.DeleteFeedback(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFeedbackFile(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, FeedbackFileName))
	if strings.Contains(string(raw), "第一个问题") {
		t.Error("删掉的反馈还留在文件里——说明不是全量重写")
	}
	// 不能留下临时文件
	if _, err := os.Stat(filepath.Join(dir, FeedbackFileName+".tmp")); err == nil {
		t.Error("临时文件没有清理掉")
	}
}
