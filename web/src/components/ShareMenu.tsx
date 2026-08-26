import { useEffect, useRef, useState } from 'react'
import { api, type DocDetail } from '../api'
import { triggerDownload } from '../download'
import { buildSelfContainedHTML, MAX_EXPORT_BYTES } from '../exportDoc'
import { relativeTime } from '../format'
import { useTouchLayout } from './useTouchLayout'

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
  const coarse = useTouchLayout()

  useEffect(() => {
    if (!open) return
    const onDown = (e: Event) => {
      if (!boxRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [open])

  const exportHTML = async () => {
    const prose = proseRef.current
    if (!prose) return
    setBusy('html')
    setNote('')
    try {
      const meta = [doc.projectName, relativeTime(doc.updatedAt), `v${doc.seq}`]
        .filter(Boolean)
        .join(' · ')
      const built = await buildSelfContainedHTML(prose, doc.title, meta)
      if (built.bytes > MAX_EXPORT_BYTES) {
        setNote(
          `这篇导出后有 ${(built.bytes / 1024 / 1024).toFixed(1)} MB，超出了上限。` +
            `图片转成内联后会比原来大约三分之一——可以改用「打印 / 存为 PDF」。`,
        )
        return
      }
      const { url } = await api.stageExport(
        `${doc.title}.html`,
        'text/html; charset=utf-8',
        built.html,
      )
      if (built.missing > 0) {
        setNote(`有 ${built.missing} 张图片已经不在库里，导出件里是空位。`)
      }
      // 不能用 location.href：那会把当前标签导航到下载地址，
      // Safari 的文件预览界面接管之后没有返回按钮，就回不到正在读的文档了。
      triggerDownload(url, `${doc.title}.html`)
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
          <button className="share-item" disabled={busy !== ''} onClick={exportHTML}>
            <span className="share-item-name">
              {busy === 'html' ? '正在生成…' : '导出单文件 HTML'}
            </span>
            <span className="share-item-hint">图片、公式、图表全在一个文件里，离线可看</span>
          </button>

          <button
            className="share-item"
            onClick={() => {
              // 必须在这里同步调用。iOS 17.4 之前，window.print() 要求处在
              // 用户手势的激活窗口内；放进 setTimeout 里手势就失效了，
              // Safari 会静默忽略——表现就是「点了没任何反应」。
              // 菜单不用手动收：打印样式里已经把 .share-menu 隐藏掉了。
              window.print()
              setOpen(false)
            }}
          >
            <span className="share-item-name">打印 / 存为 PDF</span>
            <span className="share-item-hint">
              {coarse
                ? '用系统打印面板存 PDF；没反应的话走浏览器分享面板里的「打印」'
                : '用系统的打印面板，可存成 PDF'}
            </span>
          </button>

          <button
            className="share-item"
            onClick={() => {
              // 同上：走 <a download>，页面留在原地。
              triggerDownload(api.downloadURL(doc.id))
              setOpen(false)
            }}
          >
            <span className="share-item-name">下载原始文件</span>
            <span className="share-item-hint">
              原文一字未改；带图片时会连图片打包成 zip
            </span>
          </button>
        </div>
      )}

      {note && <div className="share-note">{note}</div>}
    </div>
  )
}
