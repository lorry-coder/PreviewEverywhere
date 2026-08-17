import { KIND_LABEL, type Annotation } from '../api'

interface Props {
  annotations: Annotation[]
  activeId: number | null
  onPick: (a: Annotation) => void
  onDelete: (a: Annotation) => void
  onStartRebind: (a: Annotation) => void
}

/**
 * 右栏的批注列表。失联的单独一栏——它们没有位置可指，
 * 混在正常批注里只会让人以为高亮丢了。
 */
export default function AnnotationPanel({
  annotations,
  activeId,
  onPick,
  onDelete,
  onStartRebind,
}: Props) {
  const live = annotations.filter((a) => a.state !== 'orphan')
  const orphans = annotations.filter((a) => a.state === 'orphan')

  if (annotations.length === 0) {
    return (
      <>
        <div className="toc-group">批注</div>
        <div className="ann-empty">选中正文里的任意一段就能划重点、记笔记。</div>
      </>
    )
  }

  return (
    <>
      <div className="toc-group">批注 {live.length}</div>
      {live.map((a) => (
        <div
          key={a.id}
          className={`ann-item k-${a.kind}${a.id === activeId ? ' active' : ''}`}
          onClick={() => onPick(a)}
        >
          <div className="ann-quote">{a.quote}</div>
          {a.body && <div className="ann-body">{a.body}</div>}
          <div className="ann-meta">
            <span className="ann-kind">{KIND_LABEL[a.kind]}</span>
            {a.state === 'moved' && (
              <span className="ann-moved" title="文档改写后自动重新定位，建议复核一眼">
                已自动重定位
              </span>
            )}
            <button
              className="ann-x"
              onClick={(e) => {
                e.stopPropagation()
                onDelete(a)
              }}
            >
              删除
            </button>
          </div>
        </div>
      ))}

      {orphans.length > 0 && (
        <>
          <div className="toc-group warn">失联批注 {orphans.length}</div>
          <div className="ann-orphan-tip">
            这些批注对应的原文已经不在了。内容都留着，可以手动重挂。
          </div>
          {orphans.map((a) => (
            <div key={a.id} className="ann-item orphan">
              <div className="ann-quote">{a.quote}</div>
              {a.body && <div className="ann-body">{a.body}</div>}
              <div className="ann-meta">
                <span className="ann-kind">{KIND_LABEL[a.kind]}</span>
                <span className="ann-note">{a.orphanNote}</span>
              </div>
              <div className="ann-meta">
                <button className="ann-x" onClick={() => onStartRebind(a)}>
                  重挂
                </button>
                <button className="ann-x" onClick={() => onDelete(a)}>
                  删除
                </button>
              </div>
            </div>
          ))}
        </>
      )}
    </>
  )
}
