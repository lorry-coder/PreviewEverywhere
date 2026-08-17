import { useEffect, useRef, useState } from 'react'
import type { AnnotationKind } from '../api'
import { readSelection, type Selected } from '../annotate'

interface Props {
  proseRef: React.RefObject<HTMLElement | null>
  onCreate: (sel: Selected, kind: AnnotationKind, body: string) => Promise<void>
  /** 重挂失联批注时接管选区，不再走「新建」。 */
  rebinding: { id: number; quote: string } | null
  onRebind: (sel: Selected) => Promise<void>
  onCancelRebind: () => void
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
}: Props) {
  const [sel, setSel] = useState<Selected | null>(null)
  const [composing, setComposing] = useState<AnnotationKind | null>(null)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const popupRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // 鼠标抬起与触屏长按结束都要看一眼选区。
  useEffect(() => {
    const prose = proseRef.current
    if (!prose) return
    const check = () => {
      // 让浏览器先把选区结算完
      window.setTimeout(() => {
        if (popupRef.current?.contains(document.activeElement)) return
        const next = readSelection(prose)
        setSel(next)
        if (!next) {
          setComposing(null)
          setDraft('')
        }
      }, 10)
    }
    prose.addEventListener('mouseup', check)
    prose.addEventListener('touchend', check)
    return () => {
      prose.removeEventListener('mouseup', check)
      prose.removeEventListener('touchend', check)
    }
  }, [proseRef])

  useEffect(() => {
    if (composing) inputRef.current?.focus()
  }, [composing])

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

  // 气泡跟着选区走，但不能顶到视口外面去。
  const top = Math.max(8, sel.rect.top - 8)
  const left = Math.min(Math.max(12, sel.rect.left + sel.rect.width / 2), window.innerWidth - 12)

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
      className="sel-popup"
      ref={popupRef}
      style={{ top, left }}
      onMouseDown={(e) => e.preventDefault()} // 别让点击清掉选区
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
