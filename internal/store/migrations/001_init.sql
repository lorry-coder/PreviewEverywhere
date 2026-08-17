-- PreviewEverywhere 初始 schema。
-- 说明：annotation / tag / run 三组表在 P1 尚未被使用，但结构已按最终设计建好，
-- 目的是避免后面几期反复迁移。single-user 假设体现在 doc.read_at / read_ratio /
-- later 直接挂在 doc 上、annotation 无归属人；改多人时这两处需要拆关联表。

-- ── 项目：归档单位 ────────────────────────────────────────────────
CREATE TABLE project (
  id          INTEGER PRIMARY KEY,
  slug        TEXT    NOT NULL UNIQUE,
  name        TEXT    NOT NULL,
  root_path   TEXT,                       -- 本机监听目录；纯推送项目为 NULL
  color       TEXT,
  archived_at INTEGER,
  created_at  INTEGER NOT NULL
);

-- ── 运行：一次 agent 会话（时间轴） ───────────────────────────────
CREATE TABLE run (
  id          INTEGER PRIMARY KEY,
  project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  external_id TEXT UNIQUE,                -- Claude Code session_id 等
  label       TEXT,
  started_at  INTEGER NOT NULL,
  ended_at    INTEGER
);
CREATE INDEX idx_run_project ON run(project_id, started_at DESC);

-- ── 文档身份：跨版本稳定，批注与阅读状态挂这里 ────────────────────
CREATE TABLE doc (
  id           INTEGER PRIMARY KEY,
  project_id   INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  run_id       INTEGER REFERENCES run(id) ON DELETE SET NULL,
  source_key   TEXT    NOT NULL,          -- 项目内相对路径，或推送方给的稳定 key
  source_path  TEXT,                      -- 绝对路径，仅供展示与重新读取
  title        TEXT    NOT NULL,
  summary      TEXT,
  kind         TEXT    NOT NULL,          -- markdown | html
  render_mode  TEXT,                      -- reader | raw | NULL = 自动判定
  head_version INTEGER,                   -- 不设 FK：与 doc_version 互相引用
  read_at      INTEGER,                   -- NULL = 未读
  read_ratio   REAL    NOT NULL DEFAULT 0,
  later        INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  UNIQUE(project_id, source_key)
);
CREATE INDEX idx_doc_project ON doc(project_id, updated_at DESC);
CREATE INDEX idx_doc_updated ON doc(updated_at DESC);
CREATE INDEX idx_doc_run     ON doc(run_id);

-- ── 文档版本：不可变快照，永不覆盖 ────────────────────────────────
CREATE TABLE doc_version (
  id          INTEGER PRIMARY KEY,
  doc_id      INTEGER NOT NULL REFERENCES doc(id) ON DELETE CASCADE,
  seq         INTEGER NOT NULL,
  content_sha TEXT    NOT NULL,           -- 原文 sha256，幂等去重靠它
  raw_blob    TEXT    NOT NULL,           -- blobs/ 下的 sha256
  html        TEXT    NOT NULL,           -- 服务端渲染产物，带 data-blk
  plain       TEXT    NOT NULL,           -- 纯文本，供检索与批注锚定
  toc         TEXT    NOT NULL DEFAULT '[]',
  chars       INTEGER NOT NULL DEFAULT 0, -- 正文字符数，用于估算阅读时长
  bytes       INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  UNIQUE(doc_id, seq)
);
CREATE INDEX idx_ver_doc ON doc_version(doc_id, seq DESC);

-- ── 标签 ──────────────────────────────────────────────────────────
CREATE TABLE tag (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL UNIQUE,
  color TEXT
);

-- source 区分来源：front-matter 带来的标签在重新入库时可被覆盖，
-- 手动打的标签绝不能被 agent 的下一次生成冲掉。
CREATE TABLE doc_tag (
  doc_id INTEGER NOT NULL REFERENCES doc(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  source TEXT    NOT NULL DEFAULT 'manual',   -- manual | push | rule
  PRIMARY KEY (doc_id, tag_id)
);
CREATE INDEX idx_doc_tag_tag ON doc_tag(tag_id);

-- ── 批注（P3 启用） ───────────────────────────────────────────────
CREATE TABLE annotation (
  id            INTEGER PRIMARY KEY,
  doc_id        INTEGER NOT NULL REFERENCES doc(id) ON DELETE CASCADE,
  kind          TEXT    NOT NULL,         -- highlight | note | todo | question
  color         TEXT,
  body          TEXT,

  blk           TEXT    NOT NULL,         -- ① 命中的块 ID
  start_off     INTEGER NOT NULL,         -- ② 块内字符偏移
  end_off       INTEGER NOT NULL,
  quote_prefix  TEXT    NOT NULL,         -- ③ 引文前后文
  quote_exact   TEXT    NOT NULL,
  quote_suffix  TEXT    NOT NULL,
  doc_off       INTEGER NOT NULL,         -- ④ 全文偏移，模糊搜索起点提示

  state         TEXT    NOT NULL,         -- ok | moved | orphan
  born_version  INTEGER NOT NULL REFERENCES doc_version(id) ON DELETE CASCADE,
  bound_version INTEGER REFERENCES doc_version(id) ON DELETE SET NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_ann_doc  ON annotation(doc_id, state);
CREATE INDEX idx_ann_kind ON annotation(kind);

-- ── 内容寻址的附件存储 ────────────────────────────────────────────
CREATE TABLE blob (
  sha        TEXT PRIMARY KEY,
  mime       TEXT    NOT NULL,
  bytes      INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

-- ── 全文检索：rowid = doc.id，只索引 head 版本 ────────────────────
-- trigram 分词器让中文可用（代价：查询词需 >= 3 字符）。
CREATE VIRTUAL TABLE doc_fts USING fts5(
  title, plain, tags,
  tokenize = 'trigram'
);
