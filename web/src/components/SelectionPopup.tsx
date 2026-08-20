import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { AnnotationKind } from '../api'
import { readSelection, type Selected } from '../annotate'
import { placePopup } from '../popupPlacement'
import { useTouchLayout } from './useTouchLayout'
import { useVisualViewport } from './useVisualViewport'

interface Props {
  proseRef: React.RefObject<HTMLElement | null>
  onCreate: (sel: Selected, kind: AnnotationKind, body: string) => Promise<void>
  /** 重挂失联批注时接管选区，不再走「新建」。 */
  rebinding: { id: number; quote: string } | null
  onRebind: (sel: Selected) => Promise<void>
  onCancelRebind: () => void
  /** 有没有活跃选区。外层用它避免划词气泡和批注卡片同时贴边、叠在一起。 */
  onActiveChange?: (active: boolean) => void
}

const KINDS: { kind: AnnotationKind; label: string; needsBody: boolean }[] = [
  { kind: 'highlight', label: '高亮', needsBody: false },
  { kind: 'note', label: '笔记', needsBody: true },
  { kind: 'todo', label: '待办', needsBody: true },
  { kind: 'question', label: '疑问', needsBody: true },
]

/**
 * 划词气泡。四种类型不是随便定的：读 agent 产出时，
 * 「要去改的」和「要去确认的」是两个高频且性质不同的动作，
 * 它们会汇总成一张跨文档的清单。
 */
export default function SelectionPopup({
  proseRef,
  onCreate,
  rebinding,
  onRebind,
  onCancelRebind,
  onActiveChange,
}: Props) {
  const [sel, setSel] = useState<Selected | null>(null)
  const [composing, setComposing] = useState<AnnotationKind | null>(null)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const popupRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  // 手指正落在气泡上。iOS 点按会先把选区收掉，若跟着清空已捕获的选区，
  // 按钮就等于按不动——这个标记专门用来跨过那一瞬间。
  const touchingRef = useRef(false)
  // 浮层自身的宽度。夹紧位置时必须知道它，否则「贴着视口右边」实际上是
  // 右半个气泡已经在屏幕外了。首帧先用 CSS 里的 max-width 估一个值。
  const [width, setWidth] = useState(320)

  const coarse = useTouchLayout()
  const vv = useVisualViewport()

  // 鼠标抬起、触屏长按结束、以及选区本身发生变化，都要重新看一眼。
  useEffect(() => {
    const prose = proseRef.current
    if (!prose) return
    let timer = 0
    const check = () => {
      window.clearTimeout(timer)
      // 让浏览器先把选区结算完
      timer = window.setTimeout(() => {
        if (touchingRef.current) return
        // 正在气泡里打字：textarea 里的光标移动也会触发 selectionchange，
        // 不挡住的话写到一半会被自己关掉。
        if (popupRef.current?.contains(document.activeElement)) return
        const next = readSelection(prose)
        if (next) {
          setSel(next)
          return
        }
        setSel(null)
        setComposing(null)
        setDraft('')
      }, 60)
    }
    prose.addEventListener('mouseup', check)
    prose.addEventListener('touchend', check)
    // iOS 上拖动选区把手时 touchend 不一定落在正文元素上，
    // selectionchange 才是可靠的信号。
    document.addEventListener('selectionchange', check)
    return () => {
      window.clearTimeout(timer)
      prose.removeEventListener('mouseup', check)
      prose.removeEventListener('touchend', check)
      document.removeEventListener('selectionchange', check)
    }
  }, [proseRef])

  useEffect(() => {
    if (composing) inputRef.current?.focus()
  }, [composing])

  useEffect(() => {
    onActiveChange?.(sel !== null)
  }, [sel, onActiveChange])

  // 量一次真实宽度。用 layout effect 是为了在浏览器绘制之前就把位置改对，
  // 免得肉眼看到气泡先歪一下再跳回来。
  useLayoutEffect(() => {
    const w = popupRef.current?.getBoundingClientRect().width
    if (w && Math.abs(w - width) > 1) setWidth(w)
  })

  if (!sel) {
    return rebinding ? (
      <div className="rebind-bar">
        <span>
          正在重挂这条失联批注：<b>{rebinding.quote}</b>
        </span>
        <span className="rebind-hint">在正文里选中它现在应该指向的位置</span>
        <button className="text-btn" onClick={onCancelRebind}>
          取消
        </button>
      </div>
    ) : null
  }

  // 放哪儿由 popupPlacement 决定——那段逻辑最关键的约束（iOS 的系统选区菜单）
  // 在开发机上复现不了，所以它被拆成了不碰 DOM 的纯函数，好单独验证。
  const place = placePopup({
    coarse,
    composing: composing !== null,
    anchorTop: sel.rect.top,
    anchorBottom: sel.rect.bottom,
    anchorLeft: sel.rect.left,
    anchorWidth: sel.rect.width,
    popupWidth: width,
    viewport: { width: window.innerWidth, height: window.innerHeight },
  })
  const dockClass = place.mode === 'dock' ? ` docked ${place.edge}` : ''
  const style: React.CSSProperties =
    place.mode === 'dock'
      ? place.edge === 'top'
        ? { top: vv.top }
        : // 不是 bottom: 0。iOS 15 起 Safari 的地址栏在屏幕底部，
          // 软键盘升起时也一样——死贴 0 会让整条躲到它们后面去。
          { bottom: vv.bottom }
      : { top: place.top, left: place.left }

  const submit = async (kind: AnnotationKind) => {
    setBusy(true)
    try {
      await onCreate(sel, kind, draft.trim())
      window.getSelection()?.removeAllRanges()
      setSel(null)
      setComposing(null)
      setDraft('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className={`sel-popup${dockClass}`}
      ref={popupRef}
      style={style}
      onMouseDown={(e) => e.preventDefault()} // 桌面端：别让点击清掉选区
      // 触屏端没法靠 preventDefault 保住选区（那会连按钮的点击一起吃掉），
      // 改成打一个标记，让选区监听在这一下点击期间不要动手。
      onTouchStart={() => {
        touchingRef.current = true
      }}
      onTouchEnd={() => {
        window.setTimeout(() => {
          touchingRef.current = false
        }, 300)
      }}
    >
      {rebinding ? (
        <div className="sel-row">
          <span className="sel-label">重挂到这里？</span>
          <button
            className="sel-btn primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onRebind(sel)
                window.getSelection()?.removeAllRanges()
                setSel(null)
              } finally {
                setBusy(false)
              }
            }}
          >
            确认
          </button>
          <button className="sel-btn" onClick={onCancelRebind}>
            取消
          </button>
        </div>
      ) : composing ? (
        <div className="sel-compose">
          <textarea
            ref={inputRef}
            value={draft}
            rows={3}
            placeholder={composing === 'question' ? '想确认什么？' : '写点什么…'}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit(composing)
              if (e.key === 'Escape') setComposing(null)
            }}
          />
          <div className="sel-row">
            <button className="sel-btn primary" disabled={busy} onClick={() => submit(composing)}>
              保存
            </button>
            <button className="sel-btn" onClick={() => setComposing(null)}>
              返回
            </button>
            <span className="sel-hint">⌘↵ 保存</span>
          </div>
        </div>
      ) : (
        <div className="sel-row">
          {KINDS.map((k) => (
            <button
              key={k.kind}
              className={`sel-btn k-${k.kind}`}
              disabled={busy}
              onClick={() => (k.needsBody ? setComposing(k.kind) : submit(k.kind))}
            >
              {k.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
