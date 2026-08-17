import { useEffect, useState } from 'react'
import { api, AuthError, type Doc, type Status } from '../api'
import { relativeTime, readingTime } from '../format'
import { navigate, type Route } from '../router'

interface Props {
  route: Extract<Route, { name: 'list' }>
  status: Status | null
  reloadKey: number
}

function heading(route: Props['route'], docs: Doc[]): { title: string; sub: string } {
  if (route.unread) return { title: '未读', sub: `${docs.length} 篇待读` }
  if (route.later) return { title: '稍后读', sub: `${docs.length} 篇` }
  if (route.tag) return { title: route.tag, sub: `${docs.length} 篇带此标签` }
  if (route.project) {
    return { title: docs[0]?.projectName ?? '项目', sub: `${docs.length} 篇文档` }
  }
  return { title: '全部文档', sub: `${docs.length} 篇，按更新时间排列` }
}

export default function DocList({ route, status, reloadKey }: Props) {
  const [docs, setDocs] = useState<Doc[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setDocs(null)
    setError('')
    api
      .docs({ project: route.project, tag: route.tag, unread: route.unread, later: route.later })
      .then((d) => {
        if (!cancelled) setDocs(d)
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof AuthError) return
        setError(err instanceof Error ? err.message : '加载失败')
      })
    return () => {
      cancelled = true
    }
  }, [route.project, route.tag, route.unread, route.later, reloadKey])

  if (error) return <div className="empty">{error}</div>
  if (!docs) return <div className="loading">加载中…</div>

  const { title, sub } = heading(route, docs)

  return (
    <div className="main-inner">
      {status?.watch?.degraded && <div className="watch-warn">⚠ {status.watch.message}</div>}

      <div className="page-head">
        <h1 className="page-title">{title}</h1>
        <div className="page-sub">{sub}</div>
      </div>

      {docs.length === 0 ? <EmptyState route={route} /> : <List docs={docs} />}
    </div>
  )
}

function List({ docs }: { docs: Doc[] }) {
  return (
    <div className="doc-list">
      {docs.map((d) => (
        <button
          key={d.id}
          className={`doc-row${d.read ? '' : ' unread'}`}
          onClick={() => navigate(`#/doc/${d.id}`)}
        >
          <div className="doc-row-top">
            <span className="doc-row-title">{d.title}</span>
            <span className="doc-row-time">{relativeTime(d.updatedAt)}</span>
          </div>

          {d.summary && <div className="doc-row-summary">{d.summary}</div>}

          <div className="doc-row-meta">
            <span>{d.projectName}</span>
            <span>{readingTime(d.chars)}</span>
            {d.seq > 1 && <span className="chip v">v{d.seq}</span>}
            {d.kind === 'html' && <span className="chip">HTML</span>}
            {d.later && <span className="chip">稍后读</span>}
            {d.tags.map((t) => (
              <span key={t} className="chip">
                {t}
              </span>
            ))}
          </div>
        </button>
      ))}
    </div>
  )
}

function EmptyState({ route }: { route: Props['route'] }) {
  if (route.unread) return <div className="empty">全部读完了。</div>
  if (route.later) return <div className="empty">稍后读队列是空的。</div>
  if (route.tag) return <div className="empty">还没有文档带这个标签。</div>

  return (
    <div className="empty">
      还没有任何文档。
      <br />
      用 <code>pe watch add ~/你的项目/docs</code> 让平台自动收，
      <br />
      或者 <code>pe push 报告.md</code> 直接推一篇进来。
    </div>
  )
}
