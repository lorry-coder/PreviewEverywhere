import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { KIND_LABEL, type Annotation } from '../api'
import { rectsForAnnotation } from '../annotate'
import { placePopup } from '../popupPlacement'
import { useTouchLayout } from './useTouchLayout'

interface Props {
  annotation: Annotation
  proseRef: React.RefObject<HTMLElement | null>
  onClose: () => void
  onDelete: (a: Annotation) => Promise<void> | void
  onSaveBody: (a: Annotation, body: string) => Promise<void>
  onStartRebind: (a: Annotation) => void
}

/**
 * 点开一条已有批注。
 *
 * 在这之前，批注的正文只存在于右栏那份列表里，而右栏在 900px 以下是
 * display:none 的——也就是说手机上写完一条笔记就再也看不到它了，
 * 高亮也没有任何办法取消。这个卡片是划词之后的另一半：
 * 划词负责「记下来」，它负责「看回去、改、删掉」。
 */
export default function AnnotationPopup({
  annotation,
  proseRef,
  onClose,
  onDelete,
  onSaveBody,
  onStartRebind,
}: Props) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(annotation.body ?? '')
  const [busy, setBusy] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const [size, setSize] = useState({ width: 320 })

  const coarse = useTouchLayout()

  // 换一条批注就重置，否则会把上一条的草稿带过来。
  useEffect(() => {
    setEditing(false)
    setConfirmDel(false)
    setDraft(annotation.body ?? '')
  }, [annotation.id, annotation.body])

  useEffect(() => {
    if (editing) inputRef.current?.focus()
  }, [editing])

  useLayoutEffect(() => {
    const w = boxRef.current?.getBoundingClientRect().width
    if (w && Math.abs(w - size.width) > 1) setSize({ width: w })
  })

  // 点到别处就收起来。用 pointerdown 而不是 click：iOS 上点空白处会先
  // 收掉选区，click 有时根本不派发到 document。
  useEffect(() => {
    const onDown = (e: Event) => {
      if (boxRef.current?.contains(e.target as Node)) return
      onClose()
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [onClose])

  // 锚点用批注自己的高亮矩形。失联批注在正文里没有位置，退回视口中部，
  // 触屏上反正是贴边的，桌面端也总比定位到 (0,0) 强。
  const prose = proseRef.current
  const rects = prose
    ? rectsForAnnotation(prose, annotation.blk, annotation.startOff, annotation.endOff)
    : []
  const anchor = rects[0]
  const place = placePopup({
    coarse,
    composing: editing,
    anchorTop: anchor?.top ?? window.innerHeight / 2,
    anchorBottom: anchor?.bottom ?? window.innerHeight / 2,
    anchorLeft: anchor?.left ?? window.innerWidth / 2,
    anchorWidth: anchor?.width ?? 0,
    popupWidth: size.width,
    viewport: { width: window.innerWidth, height: window.innerHeight },
  })
  const dockClass = place.mode === 'dock' ? ` docked ${place.edge}` : ''
  const style: React.CSSProperties =
    place.mode === 'dock'
      ? place.edge === 'top'
        ? { top: 0 }
        : { bottom: 0 }
      : { top: place.top, left: place.left }

  const save = async () => {
    setBusy(true)
    try {
      await onSaveBody(annotation, draft.trim())
      setEditing(false)
    } finally {
      setBusy(false)
    }
  }

  const hasBody = (annotation.body ?? '').trim() !== ''

  return (
    <div className={`sel-popup ann-popup${dockClass}`} ref={boxRef} style={style}>
      <div className="ann-popup-inner">
        <div className="ann-popup-head">
          <span className={`chip k-${annotation.kind}`}>{KIND_LABEL[annotation.kind]}</span>
          {annotation.state === 'moved' && (
            <span className="ann-moved">已自动重定位，建议复核</span>
          )}
          {annotation.state === 'orphan' && <span className="ann-moved">原文已消失</span>}
          <button className="ann-popup-x" onClick={onClose} aria-label="关闭">
            ✕
          </button>
        </div>

        <div className="ann-popup-quote">「{annotation.quote}」</div>

        {editing ? (
          <>
            <textarea
              ref={inputRef}
              value={draft}
              rows={3}
              placeholder="写点什么…"
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) save()
                if (e.key === 'Escape') setEditing(false)
              }}
            />
            <div className="sel-row">
              <button className="sel-btn primary" disabled={busy} onClick={save}>
                保存
              </button>
              <button className="sel-btn" disabled={busy} onClick={() => setEditing(false)}>
                取消
              </button>
            </div>
          </>
        ) : (
          <>
            {hasBody ? (
              <div className="ann-popup-body">{annotation.body}</div>
            ) : (
              <div className="ann-popup-empty">这条只有高亮，没有写内容。</div>
            )}

            {confirmDel ? (
              <div className="sel-row">
                <span className="ann-popup-ask">删掉这条批注？</span>
                <button
                  className="sel-btn danger"
                  disabled={busy}
                  onClick={async () => {
                    setBusy(true)
                    try {
                      await onDelete(annotation)
                      onClose()
                    } finally {
                      setBusy(false)
                    }
                  }}
                >
                  删除
                </button>
                <button className="sel-btn" onClick={() => setConfirmDel(false)}>
                  取消
                </button>
              </div>
            ) : (
              <div className="sel-row">
                <button className="sel-btn" onClick={() => setEditing(true)}>
                  {hasBody ? '编辑' : '加内容'}
                </button>
                {annotation.state === 'orphan' && (
                  <button className="sel-btn" onClick={() => onStartRebind(annotation)}>
                    重挂
                  </button>
                )}
                <button className="sel-btn danger" onClick={() => setConfirmDel(true)}>
                  删除
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
