import { useCallback, useEffect, useRef, useState } from 'react'
import type { Annotation } from '../api'
import { rectsForAnnotation } from '../annotate'

interface Props {
  annotations: Annotation[]
  proseRef: React.RefObject<HTMLElement | null>
  /** 内容变化时用它触发重算（换文档、切「只看变化」等）。 */
  version: unknown
  activeId: number | null
  onPick: (id: number) => void
}

interface Box {
  id: number
  kind: string
  state: string
  left: number
  top: number
  width: number
  height: number
}

/**
 * 高亮层：把批注画成绝对定位的色块盖在正文上，而不是把 <mark> 塞进 DOM。
 *
 * 不改 DOM 有两个实在的好处：块索引不会因为渲染批注而失效（偏移换算一直有效），
 * 重叠的批注也不需要处理拆分。代价是布局变化时要重算矩形，
 * 用 ResizeObserver 盯着就行——它连字体和图片加载完成都能覆盖到。
 */
export default function AnnotationLayer({ annotations, proseRef, version, activeId, onPick }: Props) {
  const [boxes, setBoxes] = useState<Box[]>([])
  const layerRef = useRef<HTMLDivElement>(null)

  const recompute = useCallback(() => {
    const prose = proseRef.current
    const layer = layerRef.current
    if (!prose || !layer) return

    const origin = layer.getBoundingClientRect()
    const next: Box[] = []
    for (const a of annotations) {
      if (a.state === 'orphan') continue // 原文已消失，没有可画的位置
      for (const r of rectsForAnnotation(prose, a.blk, a.startOff, a.endOff)) {
        if (r.width < 0.5 || r.height < 0.5) continue
        next.push({
          id: a.id,
          kind: a.kind,
          state: a.state,
          left: r.left - origin.left,
          top: r.top - origin.top,
          width: r.width,
          height: r.height,
        })
      }
    }
    setBoxes(next)
  }, [annotations, proseRef])

  useEffect(() => {
    recompute()
  }, [recompute, version])

  useEffect(() => {
    const prose = proseRef.current
    if (!prose) return
    // 字体落定、图片加载、窗口缩放都会让文字重排，矩形必须跟着走。
    const observer = new ResizeObserver(() => recompute())
    observer.observe(prose)
    window.addEventListener('resize', recompute)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', recompute)
    }
  }, [proseRef, recompute])

  // 色块本身不吃鼠标事件（否则没法划词），改成在正文上做命中测试。
  useEffect(() => {
    const prose = proseRef.current
    if (!prose) return
    const onClick = (e: MouseEvent) => {
      const layer = layerRef.current
      if (!layer) return
      const origin = layer.getBoundingClientRect()
      const x = e.clientX - origin.left
      const y = e.clientY - origin.top
      for (const b of boxes) {
        if (x >= b.left && x <= b.left + b.width && y >= b.top && y <= b.top + b.height) {
          onPick(b.id)
          return
        }
      }
    }
    prose.addEventListener('click', onClick)
    return () => prose.removeEventListener('click', onClick)
  }, [boxes, proseRef, onPick])

  return (
    <div className="ann-layer" ref={layerRef} aria-hidden="true">
      {boxes.map((b, i) => (
        <div
          key={`${b.id}-${i}`}
          className={`ann-rect k-${b.kind}${b.state === 'moved' ? ' moved' : ''}${
            b.id === activeId ? ' active' : ''
          }`}
          style={{ left: b.left, top: b.top, width: b.width, height: b.height }}
        />
      ))}
    </div>
  )
}
