-- 用起来遇到问题，随手记一条。
--
-- 为什么值得单独存一张表：这个平台跑在手机上，而手机上的问题在开发机上
-- 复现不了。过去几轮排查全卡在「你那边到底是什么环境、当时发生了什么」，
-- 来回问一次就是一轮返工。所以每条反馈都连同环境快照一起存下来。
--
-- env 存 JSON 而不是拆成列：里面是前端版本、UA、视口尺寸、指针类型、
-- 安全区、是否 PWA、SW 状态这类东西，会随浏览器演进增减，
-- 拆成列意味着每加一项都要一次迁移，而我们从不按这些字段查询。
CREATE TABLE feedback (
  id         INTEGER PRIMARY KEY,
  body       TEXT    NOT NULL,                   -- 问题描述
  status     TEXT    NOT NULL DEFAULT 'open',    -- open | fixed | wontfix
  resolution TEXT,                               -- 处理说明
  -- 出问题时在读哪一篇。文档现在可以删，删了之后反馈本身还得留着。
  doc_id     INTEGER REFERENCES doc(id) ON DELETE SET NULL,
  doc_title  TEXT,                               -- 快照：文档删了也知道当时在读什么
  route      TEXT,                               -- 出问题时在哪个页面
  env        TEXT,                               -- JSON：环境快照
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_feedback_status ON feedback(status, created_at DESC);
