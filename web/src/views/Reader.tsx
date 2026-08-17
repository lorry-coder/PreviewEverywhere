import { useCallback, useEffect, useRef, useState } from 'react'
import {
  api,
  AuthError,
  parseTOC,
  type Annotation,
  type AnnotationKind,
  type DocDetail,
  type Heading,
  type Tag,
  type VersionDiff,
} from '../api'
import type { Selected } from '../annotate'
import AnnotationLayer from '../components/AnnotationLayer'
import SelectionPopup from '../components/SelectionPopup'
import TagEditor from '../components/TagEditor'
import { readingTime, relativeTime, spaceCJK } from '../format'
import { renderRichContent } from '../richContent'

interface Props {
  docId: number
  scrollRef: React.RefObject<HTMLDivElement | null>
  allTags: Tag[]
  onChanged: () => void
  /** 把拉到的文档回传给外层，供右栏使用，避免两处并发拉同一篇。 */
  onDoc: (doc: DocDetail) => void

  annotations: Annotation[]
  activeAnnotation: number | null
  onPickAnnotation: (id: number | null) => void
  onCreateAnnotation: (
    docId: number,
    sel: Selected,
    kind: AnnotationKind,
    body: string,
  ) => Promise<void>
  rebinding: Annotation | null
  onRebind: (sel: Selected) => Promise<void>
  onCancelRebind: () => void
}

export default function Reader(props: Props) {
  const { docId, scrollRef, allTags, onChanged, onDoc, annotations } = props
  const [doc, setDoc] = useState<DocDetail | null>(null)
  const [error, setError] = useState('')
  const [diff, setDiff] = useState<VersionDiff | null>(null)
  const [onlyChanged, setOnlyChanged] = useState(false)
  // 图表和公式是异步渲染的，画完之后文字重排，高亮层必须重算一次。
  const [richReady, setRichReady] = useState(0)
  const bodyRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let cancelled = false
    setDoc(null)
    setError('')
    setDiff(null)
    setOnlyChanged(false)
    api
      .doc(docId)
      .then((d) => {
        if (cancelled) return
        setDoc(d)
        onDoc(d)
      })
      .catch((err) => {
        if (cancelled || err instanceof AuthError) return
        setError(err instanceof Error ? err.message : '加载失败')
      })
    return () => {
      cancelled = true
    }
  }, [docId, onDoc])

  // 有上一版就顺手算一下差异。这是投入产出比最高的一个功能：
  // agent 重跑一次，你不必从头再读一遍。
  useEffect(() => {
    if (!doc || doc.seq < 2) return
    let cancelled = false
    api
      .diff(doc.id, doc.seq - 1, doc.seq)
      .then((d) => !cancelled && setDiff(d))
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [doc])

  // 服务端渲染不了的东西在这里补：mermaid 图与 KaTeX 公式。
  // 它们都把原始源码用 display:none 留在 DOM 里，所以批注偏移不受影响。
  // 顺带做两件纯视觉的事：中西文加间隔、外链补 target。
  useEffect(() => {
    const el = bodyRef.current
    if (!el || !doc?.html) return
    let cancelled = false
    // 排版相关的事立刻做完，不要等图表和公式——
    // 早先把 spaceCJK 放进 renderRichContent 的 finally 里，
    // mermaid 一慢，整段正文的中英文间距就一直不出现。
    spaceCJK(el)
    renderRichContent(el)
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setRichReady((n) => n + 1)
      })
    el.querySelectorAll('a[href^="http"]').forEach((a) => {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer')
    })
    return () => {
      cancelled = true
    }
  }, [doc?.html])

  // 把「哪些块变了」标到 DOM 上，「只看变化」靠它筛选。
  useEffect(() => {
    const el = bodyRef.current
    if (!el || !diff) return
    el.querySelectorAll('[data-changed]').forEach((n) => n.removeAttribute('data-changed'))
    for (const blk of diff.changed) {
      el.querySelector(`[data-blk="${CSS.escape(blk)}"]`)?.setAttribute('data-changed', '1')
    }
  }, [diff, doc?.html])

  useReadingProgress(doc, scrollRef, onChanged)

  if (error) return <div className="empty">{error}</div>
  if (!doc) return <div className="loading">加载中…</div>

  const toc = parseTOC(doc.toc)
  const isRaw = doc.renderMode === 'raw'
  const headVersion = doc.versions[0]

  return (
    <div className="reader">
      <header className="reader-head">
        <h1 className="reader-title">{doc.title}</h1>
        <div className="reader-meta">
          <span>{doc.projectName}</span>
          <span>{relativeTime(doc.updatedAt)}</span>
          {doc.chars > 0 && <span>{readingTime(doc.chars)}</span>}
          {doc.seq > 1 && <span className="chip v">v{doc.seq}</span>}
          <TagEditor
            docId={doc.id}
            tags={doc.tags}
            allTags={allTags}
            onChange={(tags) => {
              // 刻意不回传整篇文档：右栏只关心大纲与批注，
              // 而此时手里的 doc.annotations 可能已经过期。
              setDoc({ ...doc, tags })
              onChanged()
            }}
          />

          <div className="reader-actions">
            <button
              className={`text-btn${doc.later ? ' on' : ''}`}
              onClick={async () => {
                const updated = await api.patchDoc(doc.id, { later: !doc.later })
                setDoc({ ...doc, later: updated.later })
                onChanged()
              }}
            >
              {doc.later ? '已加入稍后读' : '稍后读'}
            </button>
            <button
              className={`text-btn${doc.read ? ' on' : ''}`}
              onClick={async () => {
                const updated = await api.patchDoc(doc.id, {
                  read: !doc.read,
                  readRatio: doc.read ? 0 : 1,
                })
                setDoc({ ...doc, read: updated.read })
                onChanged()
              }}
            >
              {doc.read ? '已读' : '标为已读'}
            </button>
          </div>
        </div>
      </header>

      {diff && diff.changed.length > 0 && (
        <div className="diff-bar">
          <span>
            相比 v{diff.fromSeq}：<b>{diff.changed.length}</b> 段有变化
            {diff.removed > 0 && <>，删除 {diff.removed} 段</>}
          </span>
          <button
            className={`text-btn${onlyChanged ? ' on' : ''}`}
            onClick={() => setOnlyChanged(!onlyChanged)}
          >
            {onlyChanged ? '显示全文' : '只看变化'}
          </button>
        </div>
      )}

      {isRaw && (
        <div className="notice">
          这份 HTML 带脚本或图表，正以原样模式渲染，保留了它自己的样式与交互。
          代价是这种模式下不能批注、正文也不进全文检索。
        </div>
      )}

      {isRaw && headVersion ? (
        // sandbox 只给 allow-scripts、不给 allow-same-origin：
        // iframe 处于独立的不透明源，脚本能跑，但读不到父页面 DOM 和 Cookie。
        <iframe
          className="raw-frame"
          sandbox="allow-scripts"
          src={api.rawURL(headVersion.id)}
          title={doc.title}
        />
      ) : (
        <div className="prose-wrap">
          <div
            className={`prose${onlyChanged ? ' only-changed' : ''}`}
            ref={bodyRef}
            dangerouslySetInnerHTML={{ __html: doc.html }}
          />
          <AnnotationLayer
            annotations={annotations}
            proseRef={bodyRef}
            version={`${doc.id}-${doc.seq}-${onlyChanged}-${annotations.length}-${richReady}`}
            activeId={props.activeAnnotation}
            onPick={props.onPickAnnotation}
          />
          <SelectionPopup
            proseRef={bodyRef}
            onCreate={(sel, kind, body) => props.onCreateAnnotation(doc.id, sel, kind, body)}
            rebinding={
              props.rebinding ? { id: props.rebinding.id, quote: props.rebinding.quote } : null
            }
            onRebind={props.onRebind}
            onCancelRebind={props.onCancelRebind}
          />
        </div>
      )}

      {toc.length > 0 && <MobileTOCAnchor toc={toc} />}
    </div>
  )
}

/**
 * 阅读进度跨设备同步：电脑上读到一半，手机打开自动回到那里。
 *
 * 两处容易做不对，都遇到过：
 *  1. 恢复位置不能在下一帧就设 scrollTop。图片和字体没落定时 scrollHeight
 *     还是旧值，算出来的位置是错的，表现就是「有时生效有时不生效」。
 *     这里等到高度连续两帧不变了再定位。
 *  2. 只靠防抖保存会丢进度：滚两下立刻退出，那次保存还没发出去。
 *     所以离开页面、切到后台、组件卸载时都各补一次。
 */
function useReadingProgress(
  doc: DocDetail | null,
  scrollRef: React.RefObject<HTMLDivElement | null>,
  onChanged: () => void,
) {
  const lastSent = useRef(0)
  const markedRead = useRef(false)

  useEffect(() => {
    const el = scrollRef.current
    if (!el || !doc) return

    lastSent.current = doc.readRatio
    markedRead.current = doc.read
    let disposed = false

    const ratioNow = () => {
      const max = el.scrollHeight - el.clientHeight
      return max > 8 ? Math.min(1, el.scrollTop / max) : 1
    }

    if (doc.readRatio > 0.05 && doc.readRatio < 0.95) {
      let lastHeight = -1
      let stable = 0
      let frames = 0
      const settle = () => {
        if (disposed) return
        const h = el.scrollHeight
        stable = h === lastHeight ? stable + 1 : 0
        lastHeight = h
        frames++
        if (stable >= 2 || frames > 90) {
          const max = el.scrollHeight - el.clientHeight
          if (max > 8) el.scrollTop = max * doc.readRatio
          return
        }
        requestAnimationFrame(settle)
      }
      requestAnimationFrame(settle)
    }

    const save = (opts: { force?: boolean; beacon?: boolean } = {}) => {
      const ratio = ratioNow()
      const finished = ratio >= 0.9
      const newlyRead = finished && !markedRead.current
      if (!opts.force && !newlyRead && Math.abs(ratio - lastSent.current) < 0.02) return
      lastSent.current = ratio
      if (newlyRead) markedRead.current = true

      const body = JSON.stringify({ readRatio: ratio, read: finished || undefined })
      if (opts.beacon) {
        // 页面正在被关掉，普通 fetch 会被中断。
        navigator.sendBeacon?.(
          `/api/v1/docs/${doc.id}`,
          new Blob([body], { type: 'application/json' }),
        ) ||
          fetch(`/api/v1/docs/${doc.id}`, {
            method: 'PATCH',
            body,
            keepalive: true,
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
          })
        return
      }
      api
        .patchDoc(doc.id, { readRatio: ratio, read: finished || undefined })
        .then(() => newlyRead && onChanged())
        .catch(() => {
          /* 进度同步失败不该打断阅读 */
        })
    }

    let timer: number | undefined
    const onScroll = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => save(), 600)
    }
    const onHide = () => {
      if (document.visibilityState === 'hidden') save({ beacon: true })
    }

    el.addEventListener('scroll', onScroll, { passive: true })
    document.addEventListener('visibilitychange', onHide)
    window.addEventListener('pagehide', () => save({ beacon: true }))

    // 内容不足一屏时永远不会触发滚动，直接算读完。
    if (el.scrollHeight <= el.clientHeight + 8 && !markedRead.current) {
      save({ force: true })
    }

    return () => {
      disposed = true
      window.clearTimeout(timer)
      el.removeEventListener('scroll', onScroll)
      document.removeEventListener('visibilitychange', onHide)
      // 离开这篇文档时补一次，否则「滚两下立刻返回」那次进度会丢。
      save()
    }
  }, [doc, scrollRef, onChanged])
}

/** 窄屏没有右侧大纲栏，把目录折叠到正文末尾。 */
function MobileTOCAnchor({ toc }: { toc: Heading[] }) {
  const [open, setOpen] = useState(false)
  if (window.innerWidth > 900) return null

  return (
    <div style={{ marginTop: 40, borderTop: '1px solid var(--border)', paddingTop: 16 }}>
      <button className="text-btn" onClick={() => setOpen(!open)}>
        {open ? '收起目录' : `目录（${toc.length}）`}
      </button>
      {open && (
        <div style={{ marginTop: 10 }}>
          {toc.map((h) => (
            <TOCItem key={h.blk} heading={h} />
          ))}
        </div>
      )}
    </div>
  )
}

export function TOCItem({ heading }: { heading: Heading }) {
  const jump = useCallback(() => {
    const target = document.querySelector(`[data-blk="${CSS.escape(heading.blk)}"]`)
    target?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [heading.blk])

  return (
    <button className={`toc-item lv${heading.level}`} onClick={jump} title={heading.text}>
      {heading.text}
    </button>
  )
}
