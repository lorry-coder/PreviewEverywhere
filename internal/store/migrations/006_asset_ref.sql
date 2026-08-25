-- 「文档里写的那个图片路径」对应「库里哪个 blob」。
--
-- 为什么以前没有：渲染时把 ![](./img/arch.png) 直接换成了 /api/v1/asset/<sha>，
-- 原始引用当场就丢了。看页面没问题，但要把文档连图片一起打包导出时，
-- 就不知道该把哪个 blob 放回 ./img/arch.png 这个位置。
--
-- ord 记的是图片在文档里出现的次序。它有两个用途：还原时保持稳定顺序；
-- 以及给「老文档按顺序配对」那条兜底路径一个可比对的参照。
CREATE TABLE asset_ref (
  version_id INTEGER NOT NULL REFERENCES doc_version(id) ON DELETE CASCADE,
  ord        INTEGER NOT NULL,   -- 在文档里的出现次序，从 0 起
  ref        TEXT    NOT NULL,   -- 文档里写的原始引用，如 ./img/arch.png
  sha        TEXT    NOT NULL,   -- 对应的 blob
  PRIMARY KEY (version_id, ord)
);
