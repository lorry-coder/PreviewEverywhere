import { useEffect, useRef, useState } from 'react'
import { api, type DocDetail } from '../api'
import { buildSelfContainedHTML, MAX_EXPORT_BYTES } from '../exportDoc'
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
      // 交给浏览器去下载。服务端会带 Content-Disposition，
      // iOS 上会落进「文件」，从那里就能用系统分享面板了。
      window.location.href = url
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
              setOpen(false)
              // 等菜单收起来再调，否则菜单本身会被印进去。
              window.setTimeout(() => window.print(), 120)
            }}
          >
            <span className="share-item-name">打印 / 存为 PDF</span>
            <span className="share-item-hint">用系统的打印面板，可存成 PDF</span>
          </button>

          <a className="share-item" href={api.downloadURL(doc.id)} onClick={() => setOpen(false)}>
            <span className="share-item-name">下载原始文件</span>
            <span className="share-item-hint">
              原文一字未改；带图片时会连图片打包成 zip
            </span>
          </a>
        </div>
      )}

      {note && <div className="share-note">{note}</div>}
    </div>
  )
}
