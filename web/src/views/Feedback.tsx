import { useCallback, useEffect, useState } from 'react'
import {
  api,
  AuthError,
  FEEDBACK_LABEL,
  type Feedback as FeedbackItem,
  type FeedbackStatus,
} from '../api'
import { collectEnv } from '../envSnapshot'
import { relativeTime } from '../format'
import { navigate } from '../router'

/**
 * 问题反馈。
 *
 * 提交时自动附上当时的环境快照和所在页面——这个平台跑在手机上，
 * 而手机上的问题在开发机上复现不了，事后追问「你那边是什么环境」
 * 一次就是一轮返工。
 */
export default function Feedback() {
  const [items, setItems] = useState<FeedbackItem[] | null>(null)
  const [filter, setFilter] = useState<FeedbackStatus | 'all'>('open')
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [justSent, setJustSent] = useState(false)

  const load = useCallback(() => {
    api
      .feedback(filter)
      .then(setItems)
      .catch((err) => {
        if (err instanceof AuthError) return
        setError(err instanceof Error ? err.message : '加载失败')
      })
  }, [filter])

  useEffect(load, [load])

  const submit = async () => {
    const body = draft.trim()
    if (!body) return
    setBusy(true)
    setError('')
    try {
      // 从哪里点进来的，就把哪里记下来。反馈页自己不算，
      // 记下来只会得到一堆 #/feedback。
      const from = sessionStorage.getItem('pe:lastRoute') ?? ''
      await api.createFeedback({
        body,
        route: from.startsWith('#/feedback') ? '' : from,
        env: collectEnv(),
      })
      setDraft('')
      setJustSent(true)
      window.setTimeout(() => setJustSent(false), 2500)
      setFilter('open')
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '提交失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="main-inner">
      <div className="page-head">
        <h1 className="page-title">问题反馈</h1>
        <div className="page-sub">
          用着不对劲就随手记一条。提交时会自动带上当时的设备环境和所在页面——
          这些正是事后最难补问的东西。
        </div>
      </div>

      <div className="fb-compose">
        <textarea
          rows={4}
          value={draft}
          placeholder="遇到了什么？越具体越好：在哪一步、期望是什么、实际是什么。"
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) void submit()
          }}
        />
        <div className="sel-row">
          <button className="sel-btn primary" disabled={busy || !draft.trim()} onClick={submit}>
            {busy ? '提交中…' : '提交反馈'}
          </button>
          {justSent && <span className="fb-ok">已记下</span>}
          <span className="sel-hint">⌘↵ 提交</span>
        </div>
      </div>

      {error && <div className="notice">{error}</div>}

      <div className="fb-filters">
        {(['open', 'fixed', 'wontfix', 'all'] as const).map((s) => (
          <button
            key={s}
            className={`text-btn${filter === s ? ' on' : ''}`}
            onClick={() => setFilter(s)}
          >
            {s === 'all' ? '全部' : FEEDBACK_LABEL[s]}
          </button>
        ))}
      </div>

      {!items ? (
        <div className="loading">加载中…</div>
      ) : items.length === 0 ? (
        <div className="empty">
          {filter === 'open' ? '没有待修复的问题。' : '这里还没有内容。'}
        </div>
      ) : (
        <div className="fb-list">
          {items.map((f) => (
            <FeedbackRow key={f.id} item={f} onChanged={load} />
          ))}
        </div>
      )}

      <p className="page-sub fb-foot">
        这些反馈存在数据目录里，命令行上用 <code>pe feedback</code> 也能看和处理；
        同一目录下还有一份自动生成的 <code>feedback.md</code>，直接打开就能读。
      </p>
    </div>
  )
}

function FeedbackRow({ item, onChanged }: { item: FeedbackItem; onChanged: () => void }) {
  const [open, setOpen] = useState(false)
  const [note, setNote] = useState(item.resolution ?? '')
  const [busy, setBusy] = useState(false)

  const setStatus = async (status: FeedbackStatus) => {
    setBusy(true)
    try {
      await api.patchFeedback(item.id, { status, resolution: note.trim() })
      onChanged()
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={`fb-item s-${item.status}`}>
      <div className="fb-item-head">
        <span className={`chip fb-${item.status}`}>{FEEDBACK_LABEL[item.status]}</span>
        <span className="fb-id">#{item.id}</span>
        <span className="fb-time">{relativeTime(item.createdAt)}</span>
      </div>

      <div className="fb-body">{item.body}</div>

      <div className="fb-meta">
        {item.docTitle && (
          <button
            className="text-btn"
            onClick={() => item.docId && navigate(`#/doc/${item.docId}`)}
            disabled={!item.docId}
            title={item.docId ? '打开这篇文档' : '这篇文档已经不在了'}
          >
            {item.docTitle}
          </button>
        )}
        {item.route && <code>{item.route}</code>}
        <button className="text-btn" onClick={() => setOpen(!open)}>
          {open ? '收起' : '详情'}
        </button>
      </div>

      {item.resolution && !open && <div className="fb-resolution">处理：{item.resolution}</div>}

      {open && (
        <div className="fb-detail">
          <textarea
            rows={2}
            value={note}
            placeholder="处理说明（可留空）"
            onChange={(e) => setNote(e.target.value)}
          />
          <div className="sel-row">
            <button className="sel-btn" disabled={busy} onClick={() => setStatus('fixed')}>
              标为已修复
            </button>
            <button className="sel-btn" disabled={busy} onClick={() => setStatus('wontfix')}>
              无需修复
            </button>
            <button className="sel-btn" disabled={busy} onClick={() => setStatus('open')}>
              重新打开
            </button>
          </div>
          {item.env && <EnvBlock raw={item.env} />}
        </div>
      )}
    </div>
  )
}

/** 环境快照。它是这条反馈里最省事的部分，值得完整摆出来。 */
function EnvBlock({ raw }: { raw: string }) {
  let entries: [string, string][] = []
  try {
    const m = JSON.parse(raw) as Record<string, unknown>
    entries = Object.keys(m)
      .sort()
      .map((k) => [k, String(m[k])])
  } catch {
    return <pre className="fb-env">{raw}</pre>
  }
  return (
    <div className="diag-table fb-env-table">
      {entries.map(([k, v]) => (
        <div key={k} className="diag-row">
          <span className="diag-k">{k}</span>
          <span className="diag-v">{v}</span>
        </div>
      ))}
    </div>
  )
}
