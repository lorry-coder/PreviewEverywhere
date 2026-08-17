package store

type Project struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	RootPath    string `json:"rootPath,omitempty"`
	Color       string `json:"color,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	DocCount    int    `json:"docCount"`
	UnreadCount int    `json:"unreadCount"`
}

type Doc struct {
	ID          int64    `json:"id"`
	ProjectID   int64    `json:"projectId"`
	ProjectName string   `json:"projectName,omitempty"`
	ProjectSlug string   `json:"projectSlug,omitempty"`
	SourceKey   string   `json:"sourceKey"`
	SourcePath  string   `json:"sourcePath,omitempty"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"`
	Kind        string   `json:"kind"`
	RenderMode  string   `json:"renderMode,omitempty"`
	Seq         int      `json:"seq"`
	Chars       int      `json:"chars"`
	Read        bool     `json:"read"`
	ReadRatio   float64  `json:"readRatio"`
	Later       bool     `json:"later"`
	Tags        []string `json:"tags"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// DocDetail 是阅读页需要的全部内容：文档元数据 + 渲染好的 HTML + 目录。
type DocDetail struct {
	Doc
	HTML        string       `json:"html"`
	TOC         string       `json:"toc"`
	Versions    []Version    `json:"versions"`
	Annotations []Annotation `json:"annotations"`
}

type Version struct {
	ID        int64 `json:"id"`
	Seq       int   `json:"seq"`
	Chars     int   `json:"chars"`
	Bytes     int   `json:"bytes"`
	CreatedAt int64 `json:"createdAt"`
}

type Tag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Count int    `json:"count"`
}

// SaveDocInput 是采集管线交给存储层的完整结果。
type SaveDocInput struct {
	ProjectID  int64
	RunID      int64
	SourceKey  string
	SourcePath string
	Title      string
	Summary    string
	Kind       string
	RenderMode string
	ContentSha string
	RawBlob    string
	ServeBlob  string // 内联外部依赖后的版本；为空表示直接用 RawBlob
	HTML       string
	Plain      string
	TOC        string
	Blocks     string // anchor.Block 的 JSON 数组
	Chars      int
	Bytes      int
	Tags       []string // 来自 front-matter / 推送参数，source = "push"
}

type SaveResult struct {
	DocID     int64 `json:"docId"`
	VersionID int64 `json:"versionId"`
	Seq       int   `json:"seq"`
	NewDoc    bool  `json:"newDoc"`  // 首次入库
	Changed   bool  `json:"changed"` // 内容有变化，产生了新版本
}

// DocFilter 是文档列表的查询条件。零值表示不限制。
type DocFilter struct {
	ProjectID int64
	Tag       string
	Unread    bool
	Later     bool
	Limit     int
	Offset    int
}
