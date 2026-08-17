import { useEffect, useState } from 'react'
import { api, AuthError, type Status, type TimelineGroup } from '../api'
import { readingTime, relativeTime } from '../format'
import { navigate } from '../router'

/**
 * 首页。语雀的首页是知识库封面，那套适合长期沉淀；
 * 这里面对的是持续流入的运行记录，所以首页是一条时间线——
 * 你想知道的是「昨晚那次跑出了什么」，而不是「某个项目下的全部文档」。
 */
export default function Timeline({ status, reloadKey }: { status: Status | null; reloadKey: number }) {
  const [groups, setGroups] = useState<TimelineGroup[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    api
      .timeline()
      .then((g) => !cancelled && setGroups(g))
      .catch((err) => {
        if (cancelled || err instanceof AuthError) return
        setError(err instanceof Error ? err.message : '加载失败')
      })
    return () => {
      cancelled = true
    }
  }, [reloadKey])

  if (error) return <div className="empty">{error}</div>
  if (!groups) return <div className="loading">加载中…</div>

  if (groups.length === 0) {
    return (
      <div className="main-inner">
        <div className="empty">
          还没有任何文档。
          <br />
          用 <code>pe watch add ~/你的项目/docs</code> 让平台自动收，
          <br />
          或者 <code>pe push 报告.md</code> 直接推一篇进来。
        </div>
      </div>
    )
  }

  let lastDay = ''
  return (
    <div className="main-inner">
      {status?.watch?.degraded && <div className="watch-warn">⚠ {status.watch.message}</div>}

      <div className="page-head">
        <h1 className="page-title">时间线</h1>
        <div className="page-sub">
          {status ? `共 ${status.total} 篇，${status.unread} 篇未读` : ''}
        </div>
      </div>

      <div className="timeline">
        {groups.map((g) => {
          const day = dayLabel(g.at)
          const showDay = day !== lastDay
          lastDay = day
          return (
            <div key={g.key}>
              {showDay && <div className="tl-day">{day}</div>}
              <Group group={g} />
            </div>
          )
        })}
      </div>
    </div>
  )
}

function Group({ group }: { group: TimelineGroup }) {
  return (
    <section className="tl-group">
      <header className="tl-head">
        <span className="tl-time">{clockTime(group.at)}</span>
        <button className="tl-project" onClick={() => navigate(`#/project/${group.projectId}`)}>
          {group.projectName}
        </button>
        {group.runLabel && <span className="chip">{group.runLabel}</span>}
        {group.runId ? <span className="chip v">一次运行</span> : null}
        <span className="tl-count">
          {group.docs.length} 篇
          {group.unread > 0 && <b> · {group.unread} 未读</b>}
        </span>
      </header>

      <div className="tl-docs">
        {group.docs.map((d) => (
          <button
            key={d.id}
            className={`tl-doc${d.read ? '' : ' unread'}`}
            onClick={() => navigate(`#/doc/${d.id}`)}
          >
            <span className="tl-doc-title">{d.title}</span>
            <span className="tl-doc-meta">
              {d.seq > 1 && <span className="chip v">v{d.seq}</span>}
              {d.kind === 'html' && <span className="chip">HTML</span>}
              {d.tags.slice(0, 3).map((t) => (
                <span key={t} className="chip">
                  {t}
                </span>
              ))}
              <span className="tl-doc-time">{readingTime(d.chars)}</span>
            </span>
          </button>
        ))}
      </div>
    </section>
  )
}

function dayLabel(at: number): string {
  const label = relativeTime(at)
  if (label === '刚刚' || label.endsWith('分钟前') || label.endsWith('小时前')) return '今天'
  if (label === '昨天') return '昨天'

  const d = new Date(at * 1000)
  const now = new Date()
  const md = `${d.getMonth() + 1} 月 ${d.getDate()} 日`
  return d.getFullYear() === now.getFullYear() ? md : `${d.getFullYear()} 年 ${md}`
}

function clockTime(at: number): string {
  const d = new Date(at * 1000)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
