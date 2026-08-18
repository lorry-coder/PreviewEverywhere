-- 删除文档，以及「删了之后别再自己回来」。
--
-- 平台是文件系统的下游，所以删除源文件时库里的文档是故意保留的。
-- 但反过来不成立：你在界面上主动删掉一篇，如果它的源文件还在被监听的
-- 目录里，下次启动扫描又会把它原样收回来——删除按钮变成了一个假动作。
--
-- 所以删除要留个墓碑。规则只有一条：
--   自动通道（监听、hook）遇到墓碑就跳过，显式的 pe push 则清掉墓碑重新收。
-- 「你亲手推的」比「你几个月前删过」更能代表当下的意图。
--
-- 按 (project_id, source_key) 记而不是按文档 id，因为文档行已经不在了，
-- 而下次采集时能拿到的恰好就是这两个值。
CREATE TABLE deleted_source (
  project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  source_key  TEXT    NOT NULL,
  title       TEXT,                       -- 仅供 `已忽略 N 篇` 这类提示可读
  deleted_at  INTEGER NOT NULL,
  PRIMARY KEY (project_id, source_key)
);
