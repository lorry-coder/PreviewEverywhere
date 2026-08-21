import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { AnnotationKind } from '../api'
import { readSelection, type Selected } from '../annotate'
import { placePopup } from '../popupPlacement'
import { nextStep, sameAnchor } from '../selectionCommit'
import { checkDelay, graceOnEnd, graceOnStart, inGrace } from '../touchGrace'
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

/** 位置是否基本没动。滚动一两像素不值得重渲染。 */
function sameRect(a: DOMRect, b: DOMRect): boolean {
  return Math.abs(a.top - b.top) < 1 && Math.abs(a.left - b.left) < 1 &&
    Math.abs(a.width - b.width) < 1 && Math.abs(a.height - b.height) < 1
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
  // 手指落在气泡上时的宽限期截止时间戳，语义见 touchGrace.ts。
  const graceUntilRef = useRef(0)
  // 上一次的读数（不是已提交的那个）。连续两次一致才算数，见 selectionCommit.ts。
  const lastReadRef = useRef<Selected | null>(null)
  const rechecksRef = useRef(0)
  const timerRef = useRef(0)

  // 浮层自身的尺寸。夹紧位置时必须知道它，否则「贴着视口右边」实际上是
  // 右半个气泡已经在屏幕外了。首帧先估一个值，量到真值再校正。
  const [size, setSize] = useState({ width: 260, height: 44 })

  const coarse = useTouchLayout()
  const vv = useVisualViewport()

  // 读一次当前选区。连续两次读数一致才提交——理由见 selectionCommit.ts，
  // 一句话：不急着显示，就不需要撤回，也就没有撤回带来的破坏。
  const evaluate = useCallback(() => {
    const prose = proseRef.current
    if (!prose) return

    // 正在气泡里打字：textarea 里的光标移动也会触发 selectionchange，
    // 不挡住的话写到一半会被自己关掉。
    if (popupRef.current?.contains(document.activeElement)) return

    // 手指正落在气泡上：iOS 会先收掉选区再派发 click，这段时间不做判断，
    // 但要把这次判断推迟到宽限期之后，而不是丢掉它。
    const now = Date.now()
    if (inGrace(now, graceUntilRef.current)) {
      window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(evaluate, checkDelay(now, graceUntilRef.current))
      return
    }

    const current = readSelection(prose)
    const step = nextStep(lastReadRef.current, current, rechecksRef.current, coarse)
    lastReadRef.current = current

    if (step.action === 'recheck') {
      rechecksRef.current += 1
      window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(evaluate, step.delayMs)
      return
    }

    rechecksRef.current = 0
    if (!current) {
      setSel(null)
      setComposing(null)
      setDraft('')
      return
    }
    // 身份没变时只更新位置（滚动会让 rect 移动），不换对象——
    // 少一次无谓的重渲染，就少一次在 iOS 眼皮底下动 DOM 的机会。
    setSel((prev) =>
      prev && sameAnchor(prev, current) && sameRect(prev.rect, current.rect) ? prev : current,
    )
  }, [proseRef, coarse])

  // 鼠标抬起、触屏手势结束、以及选区本身发生变化，都要重新看一眼。
  //
  // 三类事件缺一不可：selectionchange 在拖动选区把手时最可靠；
  // touchend/touchcancel 覆盖「手指抬在正文之外」（比如抬在系统菜单上）；
  // mouseup 是桌面端划词的落点。
  useEffect(() => {
    const prose = proseRef.current
    if (!prose) return
    const check = () => {
      window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(evaluate, 60)
    }
    prose.addEventListener('mouseup', check)
    document.addEventListener('touchend', check)
    document.addEventListener('touchcancel', check)
    document.addEventListener('selectionchange', check)
    return () => {
      window.clearTimeout(timerRef.current)
      prose.removeEventListener('mouseup', check)
      document.removeEventListener('touchend', check)
      document.removeEventListener('touchcancel', check)
      document.removeEventListener('selectionchange', check)
    }
  }, [proseRef, evaluate])

  useEffect(() => {
    if (composing) inputRef.current?.focus()
  }, [composing])

  useEffect(() => {
    onActiveChange?.(sel !== null)
  }, [sel, onActiveChange])

  // 量一次真实尺寸。用 layout effect 是为了在浏览器绘制之前就把位置改对，
  // 免得肉眼看到气泡先歪一下再跳回来。
  useLayoutEffect(() => {
    const r = popupRef.current?.getBoundingClientRect()
    if (!r) return
    if (Math.abs(r.width - size.width) > 1 || Math.abs(r.height - size.height) > 1) {
      setSize({ width: r.width, height: r.height })
    }
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

  // 放哪儿由 popupPlacement 决定——那段逻辑最关键的两条约束
  // （iOS 的系统选区菜单、底部地址栏）在开发机上复现不了，
  // 所以它被拆成了不碰 DOM 的纯函数，好单独验证。
  const place = placePopup({
    anchorTop: sel.rect.top,
    anchorBottom: sel.rect.bottom,
    anchorLeft: sel.rect.left,
    anchorWidth: sel.rect.width,
    popupWidth: size.width,
    popupHeight: size.height,
    view: { top: vv.top, bottom: vv.top + vv.height, width: window.innerWidth },
    avoidSystemMenu: coarse,
  })
  const style: React.CSSProperties = { top: place.top, left: place.left }

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
      className={`sel-popup side-${place.side}${coarse ? ' touch' : ''}`}
      ref={popupRef}
      style={style}
      onMouseDown={(e) => e.preventDefault()} // 桌面端：别让点击清掉选区
      // 触屏端没法靠 preventDefault 保住选区（那会连按钮的点击一起吃掉），
      // 改成打一个标记，让选区监听在这一下点击期间不要动手。
      onTouchStart={() => {
        graceUntilRef.current = graceOnStart(Date.now())
      }}
      onTouchEnd={() => {
        graceUntilRef.current = graceOnEnd(Date.now())
      }}
      onTouchCancel={() => {
        graceUntilRef.current = graceOnEnd(Date.now())
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
