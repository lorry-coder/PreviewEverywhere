import { useEffect, useState } from 'react'
import { api, AuthError, KIND_LABEL, type Annotation } from '../api'
import { copyText } from '../clipboard'
import { relativeTime } from '../format'
import { navigate } from '../router'

/**
 * 待办与疑问的跨文档汇总。
 *
 * 这是「在通勤路上读文档」这件事的产出物：你散落在十几份文档里的
 * 待办和疑问，回到电脑前是一张清单。导出的 Markdown 可以直接
 * 粘给下一轮 agent 当输入。
 */
export default function Actionable({ reloadKey }: { reloadKey: number }) {
  const [items, setItems] = useState<Annotation[] | null>(null)
  const [error, setError] = useState('')
  // '' | 'ok' | 'fail'。此前不管成没成都显示「已复制」——
  // 局域网是 http，剪贴板接口根本不存在，那句提示纯属骗人。
  const [copyState, setCopyState] = useState<'' | 'ok' | 'fail'>('')
  const [manual, setManual] = useState('')
  const [busy, setBusy] = useState<number | null>(null)

  useEffect(() => {
    let cancelled = false
    api
      .actionable()
      .then((list) => !cancelled && setItems(list))
      .catch((err) => {
        if (cancelled || err instanceof AuthError) return
        setError(err instanceof Error ? err.message : '加载失败')
      })
    return () => {
      cancelled = true
    }
  }, [reloadKey])

  if (error) return <div className="empty">{error}</div>
  if (!items) return <div className="loading">加载中…</div>

  const todos = items.filter((a) => a.kind === 'todo')
  const questions = items.filter((a) => a.kind === 'question')

  const asMarkdown = () =>
    items
      .map(
        (a) =>
          `- [${KIND_LABEL[a.kind]}] ${a.body || a.quote}\n` +
          `  - 出处：${a.projectName} / ${a.docTitle}\n` +
          `  - 原文：${a.quote}`,
      )
      .join('\n')

  return (
    <div className="main-inner">
      <div className="page-head">
        <h1 className="page-title">待办与疑问</h1>
        <div className="page-sub">
          {todos.length} 条待办 · {questions.length} 条疑问
        </div>
      </div>

      {items.length === 0 ? (
        <div className="empty">
          还没有待办或疑问。
          <br />
          读文档时选中一段，选「待办」或「疑问」，就会汇总到这里。
        </div>
      ) : (
        <>
          <div className="actionable-bar">
            <button
              className="text-btn"
              onClick={async () => {
                const text = asMarkdown()
                // 无论成没成都把原文摊出来。iOS 上 execCommand 返回 true
                // 也可能压根没放进剪贴板，只信那个布尔值就会出现
                // 「显示已复制、粘出来是空的」——那比不复制更糟。
                setCopyState((await copyText(text)) ? 'ok' : 'fail')
                setManual(text)
              }}
            >
              {copyState === 'ok' ? '已复制' : '复制为 Markdown'}
            </button>
            <span className="actionable-hint">
              {copyState === '' && '复制出来可以直接粘给下一轮 agent'}
              {copyState === 'ok' && '已尝试复制。iOS 上不一定真的成功，原文也留在下面。'}
              {copyState === 'fail' &&
                '这个页面走的是 http，浏览器不提供剪贴板接口。下面这段长按全选即可。'}
            </span>
          </div>

          {manual && (
            <div className="diag-manual">
              <textarea readOnly rows={10} value={manual} onFocus={(e) => e.target.select()} />
            </div>
          )}

          <div className="doc-list">
            {items.map((a) => (
              // 用 div 而不是 button：里面还要放一个删除按钮，
              // button 套 button 是非法结构，Safari 上点击行为也会变得不确定。
              <div
                key={a.id}
                className="doc-row"
                role="button"
                tabIndex={0}
                onClick={() => navigate(`#/doc/${a.docId}`)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') navigate(`#/doc/${a.docId}`)
                }}
              >
                <div className="doc-row-top">
                  <span className="doc-row-title">{a.body || a.quote}</span>
                  <span className="doc-row-time">{relativeTime(a.createdAt)}</span>
                </div>
                <div className="doc-row-summary">「{a.quote}」</div>
                <div className="doc-row-meta">
                  <span className={`chip k-${a.kind}`}>{KIND_LABEL[a.kind]}</span>
                  <span>{a.projectName}</span>
                  <span>{a.docTitle}</span>
                  {a.state === 'orphan' && <span className="chip warn">原文已消失</span>}
                  {/* 待办做完了要能划掉。没有这个的话这张清单只进不出，
                      用两天就没人看了。 */}
                  <button
                    className="row-x"
                    disabled={busy === a.id}
                    onClick={async (e) => {
                      e.stopPropagation()
                      setBusy(a.id)
                      try {
                        await api.deleteAnnotation(a.id)
                        setItems((prev) => (prev ?? []).filter((x) => x.id !== a.id))
                      } catch (err) {
                        setError(err instanceof Error ? err.message : '删除失败')
                      } finally {
                        setBusy(null)
                      }
                    }}
                  >
                    {busy === a.id ? '删除中…' : '完成并删除'}
                  </button>
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
