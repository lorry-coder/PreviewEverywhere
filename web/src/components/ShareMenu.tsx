import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, type DocDetail } from '../api'
import { isStandalone, triggerDownload } from '../download'
import { buildSelfContainedHTML, MAX_EXPORT_BYTES, type ExportFlavour } from '../exportDoc'
import { copyText } from '../clipboard'
import { relativeTime } from '../format'

/**
 * 分享菜单：把这一篇带走的三条路。
 *
 * 为什么不是「系统分享面板」：navigator.share 需要安全上下文，而这个平台
 * 的正常用法是局域网 http，那里它根本不存在（剪贴板也是同样的原因）。
 * 所以分享只能落到「下载成文件」，再由系统的文件应用去分享。
 */
/**
 * 这篇文档的完整地址，用来在 Safari 里打开。
 *
 * 刻意自己拼而不是用 location.href：扫码登录落地时地址里带过访问口令，
 * 虽然前端随后就把它抹掉了，但拼一个干净的出来更稳妥——
 * 这行字是要被复制、被转发的。
 */
/** 点一下整行选中。推迟一拍是因为焦点的默认行为会把当场设好的选区覆盖掉。 */
function selectAllSoon(e: React.FocusEvent<HTMLInputElement> | React.MouseEvent<HTMLInputElement>) {
  const el = e.currentTarget
  window.setTimeout(() => el.setSelectionRange(0, el.value.length), 0)
}

function docURL(id: number): string {
  return `${location.origin}${location.pathname}#/doc/${id}`
}

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
  const [urlCopied, setUrlCopied] = useState(false)
  // 菜单是 fixed 定位（横向贴视口右缘，才不会被窄屏切掉），
  // 所以纵向位置得自己量：贴在「分享」按钮下面。
  const [menuTop, setMenuTop] = useState(0)
  const btnRef = useRef<HTMLButtonElement>(null)
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

  useEffect(() => {
    if (!open) setUrlCopied(false)
  }, [open])

  useLayoutEffect(() => {
    if (!open) return
    const place = () => {
      const r = btnRef.current?.getBoundingClientRect()
      if (r) setMenuTop(r.bottom + 6)
    }
    place()
    // 菜单是 fixed 的，页面一滚它就会脱离按钮。与其让它飘着，不如收起来。
    const onScroll = () => setOpen(false)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', place)
    return () => {
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', place)
    }
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
      <button
        ref={btnRef}
        className={`text-btn${open ? ' on' : ''}`}
        onClick={() => setOpen(!open)}
      >
        分享
      </button>

      {open && (
        <div className="share-menu" style={{ top: menuTop }}>
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

          {standalone && (
            <div className="share-safari">
              <p>
                要把文件真正存下来，请在 <b>Safari</b> 里打开下面这个地址——
                主屏 App 模式下 iOS 不支持下载文件。
              </p>
              <input
                readOnly
                value={docURL(doc.id)}
                // 点一下就整行选中，省掉手动拖两个把手。
                //
                // 必须推迟一拍：在 focus 处理里设选区会被浏览器随后的默认行为
                // （把光标放到末尾）覆盖掉，实测就是这样，当场设完读回来是 0。
                onFocus={selectAllSoon}
                onClick={selectAllSoon}
                // 点地址栏不该把菜单收起来
                onPointerDown={(e) => e.stopPropagation()}
              />
              <div className="sel-row">
                <button
                  className="sel-btn"
                  onClick={async () => {
                    // 局域网是 http，剪贴板接口可能根本不存在；复制不成也没关系，
                    // 上面那个只读框长按就能全选，所以这里如实反映成败即可。
                    setUrlCopied(await copyText(docURL(doc.id)))
                    window.setTimeout(() => setUrlCopied(false), 2500)
                  }}
                >
                  {urlCopied ? '已复制' : '复制地址'}
                </button>
                <span className="sel-hint">复制不成就长按上面那行全选</span>
              </div>
            </div>
          )}
        </div>
      )}

      {note && <div className="share-note">{note}</div>}
    </div>
  )
}
