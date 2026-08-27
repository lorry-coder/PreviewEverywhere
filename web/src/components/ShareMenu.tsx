import { useEffect, useRef, useState } from 'react'
import { api, type DocDetail } from '../api'
import { isStandalone, triggerDownload } from '../download'
import { buildSelfContainedHTML, MAX_EXPORT_BYTES, type ExportFlavour } from '../exportDoc'
import { relativeTime } from '../format'

/**
 * 分享菜单：把这一篇带走的三条路。
 *
 * 为什么不是「系统分享面板」：navigator.share 需要安全上下文，而这个平台
 * 的正常用法是局域网 http，那里它根本不存在（剪贴板也是同样的原因）。
 * 所以分享只能落到「下载成文件」，再由系统的文件应用去分享。
 */
export default function ShareMenu({
  doc,
  proseRef,
}: {
  doc: DocDetail
  proseRef: React.RefObject<HTMLElement | null>
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')
  const boxRef = useRef<HTMLDivElement>(null)
  // 「加到主屏」之后 Safari 不实现 window.print()，点了没有任何反应。
  // 与其留一个死按钮，不如在这个模式下直接不显示——PDF 由「导出 PDF」产出。
  const standalone = isStandalone()

  useEffect(() => {
    if (!open) return
    const onDown = (e: Event) => {
      if (!boxRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [open])

  const runExport = async (flavour: ExportFlavour) => {
    const prose = proseRef.current
    if (!prose) return
    setBusy(flavour)
    setNote('')
    try {
      const meta = [doc.projectName, relativeTime(doc.updatedAt), `v${doc.seq}`]
        .filter(Boolean)
        .join(' · ')
      const built = await buildSelfContainedHTML(prose, doc.title, meta, flavour)
      if (built.bytes > MAX_EXPORT_BYTES) {
        setNote(
          `这篇导出后有 ${(built.bytes / 1024 / 1024).toFixed(1)} MB，超出了上限。` +
            `图片转成内联后会比原来大约三分之一，可以试试只导出其中一部分。`,
        )
        return
      }
      const { url } = await api.stageExport(
        `${doc.title}.${flavour}`,
        flavour === 'pdf' ? 'application/pdf' : 'text/html; charset=utf-8',
        built.html,
        flavour === 'pdf' ? 'pdf' : undefined,
      )
      if (built.missing > 0) {
        setNote(`有 ${built.missing} 张图片已经不在库里，导出件里是空位。`)
      }
      triggerDownload(url, `${doc.title}.${flavour}`)
      setOpen(false)
    } catch (err) {
      setNote(err instanceof Error ? err.message : '导出失败')
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="share-wrap" ref={boxRef}>
      <button className={`text-btn${open ? ' on' : ''}`} onClick={() => setOpen(!open)}>
        分享
      </button>

      {open && (
        <div className="share-menu">
          <button
            className="share-item"
            disabled={busy !== ''}
            onClick={() => void runExport('pdf')}
          >
            <span className="share-item-name">{busy === 'pdf' ? '正在生成…' : '导出 PDF'}</span>
            <span className="share-item-hint">
              {standalone
                ? '打开后用右上角的分享按钮保存或转发'
                : '文字可选可搜；图表会转成图片，因此不可选'}
            </span>
          </button>

          <button
            className="share-item"
            disabled={busy !== ''}
            onClick={() => void runExport('html')}
          >
            <span className="share-item-name">
              {busy === 'html' ? '正在生成…' : '导出单文件 HTML'}
            </span>
            <span className="share-item-hint">
              {standalone
                ? '主屏 App 里只能打开看，存不下来——想保存请用「导出 PDF」'
                : '图片、公式、图表全在一个文件里，离线可看'}
            </span>
          </button>

          {!standalone && (
            <button
              className="share-item"
              onClick={() => {
                // 必须在这里同步调用。iOS 17.4 之前，window.print() 要求处在
                // 用户手势的激活窗口内；放进 setTimeout 里手势就失效了。
                // 菜单不用手动收：打印样式里已经把 .share-menu 隐藏掉了。
                window.print()
                setOpen(false)
              }}
            >
              <span className="share-item-name">用系统打印</span>
              <span className="share-item-hint">走浏览器自带的打印面板</span>
            </button>
          )}

          <button
            className="share-item"
            onClick={() => {
              triggerDownload(api.downloadURL(doc.id))
              setOpen(false)
            }}
          >
            <span className="share-item-name">下载原始文件</span>
            <span className="share-item-hint">
              {standalone
                ? '主屏 App 里下载不了；要拿源码请在 Safari 里打开本站'
                : '原文一字未改；带图片时会连图片打包成 zip'}
            </span>
          </button>
        </div>
      )}

      {note && <div className="share-note">{note}</div>}
    </div>
  )
}
