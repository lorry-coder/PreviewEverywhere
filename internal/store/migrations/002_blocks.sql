-- 每个版本额外存一份「块索引」：块 ID 及其在纯文本里的偏移与长度。
--
-- 批注重定位需要在「块 ID」和「全文字符偏移」之间来回换算：
-- 命中块 ID 时要知道块在全文的哪一段，模糊匹配到全文某个位置时
-- 又要知道它落在哪个块里。每次重新解析 HTML 去算太浪费，
-- 而且解析结果必须和当初渲染时逐字一致，直接存下来最稳。
--
-- 形如 [{"b":"a7f3k2qd","o":0,"l":24}, ...]，o 与 l 都是字符数而非字节数。
ALTER TABLE doc_version ADD COLUMN blocks TEXT NOT NULL DEFAULT '[]';

-- 失联批注要展示「当初那段原文长什么样」，否则用户根本无从判断该重挂到哪。
ALTER TABLE annotation ADD COLUMN orphan_note TEXT;
