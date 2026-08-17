import { useEffect, useState } from 'react'
import { api, AuthError, type ParsedQuery, type Project, type SearchHit, type Tag } from '../api'
import { relativeTime } from '../format'
import { navigate } from '../router'

export default function SearchResults({ q, tags, projects }: {
  q: string
  tags: Tag[]
  projects: Project[]
}) {
  const [hits, setHits] = useState<SearchHit[] | null>(null)
  const [parsed, setParsed] = useState<ParsedQuery | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!q.trim()) {
      setHits([])
      setParsed(null)
      return
    }
    let cancelled = false
    setError('')
    api
      .search(q)
      .then((res) => {
        if (cancelled) return
        setHits(res.hits)
        setParsed(res.query)
      })
      .catch((err) => {
        if (cancelled || err instanceof AuthError) return
        setError(err instanceof Error ? err.message : '检索失败')
      })
    return () => {
      cancelled = true
    }
  }, [q])

  if (error) return <div className="empty">{error}</div>

  return (
    <div className="main-inner">
      <div className="page-head">
        <h1 className="page-title">搜索</h1>
        <div className="page-sub">
          {q.trim() === ''
            ? '输入关键词，或用 tag: / project: / is: 缩小范围'
            : hits === null
              ? '检索中…'
              : `${hits.length} 条结果`}
        </div>
      </div>

      {parsed && <ParsedHint query={parsed} />}

      {q.trim() === '' ? (
        <SyntaxHelp tags={tags} projects={projects} />
      ) : hits === null ? (
        <div className="loading">检索中…</div>
      ) : hits.length === 0 ? (
        <div className="empty">
          没有匹配的文档。
          <br />
          中文两字词（如「双写」）走的是全表匹配，三字以上才走索引，都能搜到。
        </div>
      ) : (
        <div className="doc-list">
          {hits.map((h) => (
            <button
              key={h.id}
              className={`doc-row${h.read ? '' : ' unread'}`}
              onClick={() => navigate(`#/doc/${h.id}`)}
            >
              <div className="doc-row-top">
                <span className="doc-row-title">{h.title}</span>
                <span className="doc-row-time">{relativeTime(h.updatedAt)}</span>
              </div>
              {/* snippet 由服务端转义好，只留自己加的 <mark> */}
              <div
                className="doc-row-summary snippet"
                dangerouslySetInnerHTML={{ __html: h.snippet }}
              />
              <div className="doc-row-meta">
                <span>{h.projectName}</span>
                {h.seq > 1 && <span className="chip v">v{h.seq}</span>}
                {h.tags.map((t) => (
                  <span key={t} className="chip">
                    {t}
                  </span>
                ))}
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** 把解析结果显示出来，让用户知道 `tag:` 这类前缀确实生效了。 */
function ParsedHint({ query }: { query: ParsedQuery }) {
  const bits: string[] = []
  query.Tags?.forEach((t) => bits.push(`标签 ${t}`))
  query.NotTags?.forEach((t) => bits.push(`排除标签 ${t}`))
  if (query.Project) bits.push(`项目 ${query.Project}`)
  if (query.Kind) bits.push(`类型 ${query.Kind}`)
  if (query.Unread) bits.push('未读')
  if (query.Read) bits.push('已读')
  if (query.Later) bits.push('稍后读')
  if (bits.length === 0) return null

  return (
    <div className="parsed-hint">
      {bits.map((b) => (
        <span key={b} className="chip">
          {b}
        </span>
      ))}
      {query.Terms && query.Terms.length > 0 && <span>关键词 {query.Terms.join(' ')}</span>}
    </div>
  )
}

/**
 * 语法帮助。每一行都可以直接点——照着敲一遍语法太费事，
 * 点一下把它填进搜索框、顺手跑一次，才是真的在教人用。
 */
function SyntaxHelp({ tags, projects }: { tags: Tag[]; projects: Project[] }) {
  // 例子用库里真实存在的标签和项目，点下去才有结果，而不是演示一个空查询。
  const tag = tags[0]?.name ?? '待复核'
  const otherTag = tags[1]?.name ?? '已解决'
  const project = projects[0]?.slug ?? 'auth'

  const rows: [string, string][] = [
    [`tag:${tag}`, '带某个标签'],
    [`tag:${tag} -tag:${otherTag}`, '组合与排除'],
    [`project:${project}`, '限定在某个项目里'],
    ['is:unread', '只看未读'],
    ['is:later', '只看稍后读'],
    ['kind:html', '只看 HTML 文档'],
    ['"整段短语"', '引号内不拆词'],
  ]

  return (
    <>
      <div className="syntax-tip">点任意一行即可直接试用：</div>
      <div className="syntax-help">
        {rows.map(([syntax, desc]) => (
          <button
            key={syntax}
            className="syntax-row"
            onClick={() => navigate(`#/search/${encodeURIComponent(syntax)}`)}
          >
            <code>{syntax}</code>
            <span>{desc}</span>
          </button>
        ))}
      </div>
    </>
  )
}
