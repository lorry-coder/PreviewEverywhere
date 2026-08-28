import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import {
  api,
  AuthError,
  KIND_LABEL,
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
import AnnotationPopup from '../components/AnnotationPopup'
import ShareMenu from '../components/ShareMenu'
import SelectionPopup from '../components/SelectionPopup'
import TagEditor from '../components/TagEditor'
import { readingTime, relativeTime, spaceCJK } from '../format'
import { navigate } from '../router'
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
  onDeleteAnnotation: (a: Annotation) => Promise<void> | void
  onUpdateAnnotationBody: (a: Annotation, body: string) => Promise<void>
  onStartRebind: (a: Annotation) => void
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
  // 正在划词。两个浮层都会贴到屏幕边缘，同时出现就是叠在一起，
  // 谁也点不准——划词优先，它是当下正在进行的动作。
  const [selecting, setSelecting] = useState(false)

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
  // 正文内容由我们自己写进 DOM，不交给 React 的 dangerouslySetInnerHTML。
  //
  // 为什么：实测抓到过 React 用**完全相同的字符串**重设 .prose 的 innerHTML
  // ——一次纯冗余的更新。桌面上看不出任何异常，但在 iOS 上，正文 DOM 一被
  // 整体换掉，系统就会丢掉正在进行的选区。表现就是长按选中之后标记突然消失、
  // 气泡随即也没了，而且时有时无（取决于那次重设发生在手势的哪一步）。
  //
  // 交给 React 管，就得赌它永远不做多余的更新；自己写，这件事就不可能发生。
  // 顺带还修掉一个隐患：spaceCJK 和 renderRichContent 是直接改 DOM 的，
  // React 一旦重设 innerHTML 就会把它们的成果抹掉，而依赖没变、effect 不会
  // 重跑——中西文间距和图表会无声消失。
  //
  // 用 layout effect 而不是 effect：在浏览器绘制之前写完，避免闪一下空白。
  useLayoutEffect(() => {
    const el = bodyRef.current
    if (el && doc?.html != null && el.innerHTML !== doc.html) {
      el.innerHTML = doc.html
    }
  }, [doc?.html])

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
  const activeAnn = annotations.find((a) => a.id === props.activeAnnotation) ?? null

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
            <ShareMenu doc={doc} proseRef={bodyRef} />
            <DeleteButton doc={doc} onDeleted={onChanged} />
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
          {/* 正文容器刻意留空，内容由下面那个 layout effect 自己塞进去。
              理由见那里——一句话：不能让 React 有机会重设它的 innerHTML。 */}
          <div className={`prose${onlyChanged ? ' only-changed' : ''}`} ref={bodyRef} />
          <AnnotationLayer
            annotations={annotations}
            proseRef={bodyRef}
            version={`${doc.id}-${doc.seq}-${onlyChanged}-${annotations.length}-${richReady}`}
            activeId={props.activeAnnotation}
            onPick={props.onPickAnnotation}
          />
          {/* 点开一条已有批注：看内容、改、删。
              右栏在 900px 以下是隐藏的，没有这个卡片，手机上写完一条笔记
              就再也看不到它，高亮也没法取消。 */}
          {activeAnn && !props.rebinding && !selecting && (
            <AnnotationPopup
              annotation={activeAnn}
              proseRef={bodyRef}
              onClose={() => props.onPickAnnotation(null)}
              onDelete={props.onDeleteAnnotation}
              onSaveBody={props.onUpdateAnnotationBody}
              onStartRebind={props.onStartRebind}
            />
          )}
          <SelectionPopup
            proseRef={bodyRef}
            onCreate={(sel, kind, body) => props.onCreateAnnotation(doc.id, sel, kind, body)}
            rebinding={
              props.rebinding ? { id: props.rebinding.id, quote: props.rebinding.quote } : null
            }
            onRebind={props.onRebind}
            onCancelRebind={props.onCancelRebind}
            onActiveChange={setSelecting}
          />
        </div>
      )}

      <MobileAnchor toc={toc} annotations={annotations} onPick={props.onPickAnnotation} />
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
/**
 * 删除按钮。二次确认不是走个形式：批注和版本历史会一起消失，而它们是
 * 这个平台里唯一无法从别处恢复的东西——源文件还能重新采集，你写的批注不能。
 */
function DeleteButton({ doc, onDeleted }: { doc: DocDetail; onDeleted: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const [forget, setForget] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!confirming) {
    return (
      <button className="text-btn danger" onClick={() => setConfirming(true)}>
        删除
      </button>
    )
  }

  const annotationCount = doc.annotations?.length ?? 0

  return (
    <div className="delete-confirm">
      <p>
        删掉这篇？
        {annotationCount > 0 && (
          <>
            {' '}
            <b>{annotationCount} 条批注</b>会一起消失，无法恢复。
          </>
        )}
        {doc.sourcePath && ' 磁盘上的源文件不受影响。'}
      </p>
      <label className="delete-forget">
        <input type="checkbox" checked={forget} onChange={(e) => setForget(e.target.checked)} />
        以后源文件再被扫到时还收进来
      </label>
      {!forget && doc.sourcePath && (
        <p className="delete-hint">
          默认会记住「这篇不要了」，否则下次扫描到同一个文件又会把它收回来。
        </p>
      )}
      {error && <p className="delete-error">{error}</p>}
      <div className="delete-actions">
        <button
          className="text-btn danger"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              await api.deleteDoc(doc.id, forget)
              onDeleted()
              navigate('#/all')
            } catch (e) {
              setError(e instanceof Error ? e.message : '删除失败')
              setBusy(false)
            }
          }}
        >
          {busy ? '删除中…' : '确认删除'}
        </button>
        <button className="text-btn" disabled={busy} onClick={() => setConfirming(false)}>
          取消
        </button>
      </div>
    </div>
  )
}

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
/**
 * 窄屏下的目录与批注入口。
 *
 * 右栏（大纲 + 批注）在 900px 以下整个 display:none，所以手机上必须
 * 另开一个口子。放在正文末尾是因为它更像「读完之后回看」的动作；
 * 单条批注的即时查看走点高亮弹卡片那条路。
 */
function MobileAnchor({
  toc,
  annotations,
  onPick,
}: {
  toc: Heading[]
  annotations: Annotation[]
  onPick: (id: number) => void
}) {
  const [open, setOpen] = useState<'toc' | 'ann' | null>(null)
  const narrow = useNarrowViewport()
  if (!narrow) return null
  const live = annotations.filter((a) => a.state !== 'orphan')
  if (toc.length === 0 && annotations.length === 0) return null

  return (
    <div className="mobile-anchor">
      <div className="sel-row">
        {toc.length > 0 && (
          <button
            className={`text-btn${open === 'toc' ? ' on' : ''}`}
            onClick={() => setOpen(open === 'toc' ? null : 'toc')}
          >
            目录（{toc.length}）
          </button>
        )}
        {annotations.length > 0 && (
          <button
            className={`text-btn${open === 'ann' ? ' on' : ''}`}
            onClick={() => setOpen(open === 'ann' ? null : 'ann')}
          >
            我的批注（{annotations.length}）
          </button>
        )}
      </div>

      {open === 'toc' && (
        <div className="mobile-anchor-body">
          {toc.map((h) => (
            <TOCItem key={h.blk} heading={h} />
          ))}
        </div>
      )}

      {open === 'ann' && (
        <div className="mobile-anchor-body">
          {annotations.map((a) => (
            <button
              key={a.id}
              className={`ann-item k-${a.kind}`}
              onClick={() => {
                onPick(a.id)
                document
                  .querySelector(`[data-blk="${CSS.escape(a.blk)}"]`)
                  ?.scrollIntoView({ behavior: 'smooth', block: 'center' })
              }}
            >
              <div className="ann-quote">{a.quote}</div>
              {a.body && <div className="ann-body">{a.body}</div>}
              <div className="ann-meta">
                <span className="ann-kind">{KIND_LABEL[a.kind]}</span>
                {a.state === 'orphan' && <span className="ann-moved">原文已消失</span>}
              </div>
            </button>
          ))}
          {live.length !== annotations.length && (
            <div className="ann-orphan-tip">
              「原文已消失」的批注内容都留着，点开可以删掉或重挂。
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/** 窄屏判断。原先写成渲染时读一次 window.innerWidth，转屏之后不会更新。 */
function useNarrowViewport(): boolean {
  const [narrow, setNarrow] = useState(
    () => window.matchMedia?.('(max-width: 900px)').matches ?? false,
  )
  useEffect(() => {
    const mq = window.matchMedia?.('(max-width: 900px)')
    if (!mq) return
    const onChange = () => setNarrow(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return narrow
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
