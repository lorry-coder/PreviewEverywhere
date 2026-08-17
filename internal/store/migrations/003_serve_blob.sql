-- 原样模式的 HTML 在入库时会把 CDN 依赖内联进来（见 ingest/localize.go），
-- 于是「原始文件」和「实际提供给 iframe 的文件」不再是同一份。
--
-- 两份都留着：raw_blob 是一字未改的原文，serve_blob 是内联后的版本。
-- 这样既不违背「平台是文件系统的下游、原文照留」这条原则，
-- 又能让你在地铁上打开时图表真的画得出来。
ALTER TABLE doc_version ADD COLUMN serve_blob TEXT;
