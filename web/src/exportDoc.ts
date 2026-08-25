/**
 * 把当前正在读的这一篇导出成一个自包含的 HTML 文件。
 *
 * 为什么在浏览器里做而不是服务端：页面上此刻的样子才是完整的——mermaid
 * 已经画成 SVG、公式已经排好版。服务端只有一份还没执行过 JS 的 HTML，
 * 导出去图表就只剩一段代码。
 *
 * 为什么导出 HTML 而不是原始 md：md 里写的是 ![](./img/arch.png)，
 * 图片并不在文件里，发给别人打开就是一堆裂图。把图片内联成 data URI
 * 之后，一个文件就是全部，离线也能看。
 */

/** 单份导出的上限。data URI 会让体积比原始图片大约三分之一。 */
export const MAX_EXPORT_BYTES = 48 * 1024 * 1024

export interface ExportResult {
  html: string
  bytes: number
  images: number
  /** 有几张图没能内联进来（已经不在库里，或者本来就是坏的引用） */
  missing: number
}

/**
 * 用页面上已渲染的正文生成自包含 HTML。
 *
 * @param proseEl  正文容器（.prose）
 * @param title    文档标题
 * @param meta     副标题那一行，如「项目 · 更新时间」
 */
export async function buildSelfContainedHTML(
  proseEl: HTMLElement,
  title: string,
  meta: string,
): Promise<ExportResult> {
  const clone = proseEl.cloneNode(true) as HTMLElement

  // 批注高亮层是绝对定位盖上去的，克隆出来会变成一堆错位色块。
  clone.querySelectorAll('.ann-layer, .ann-rect').forEach((el) => el.remove())
  // 显示用的辅助属性带出去没有意义，还会让文件变大。
  clone.querySelectorAll('[data-blk]').forEach((el) => el.removeAttribute('data-blk'))

  let images = 0
  let missing = 0
  const imgs = Array.from(clone.querySelectorAll('img'))
  for (const img of imgs) {
    const src = img.getAttribute('src') ?? ''
    if (!src || src.startsWith('data:')) continue
    images++
    const data = await toDataURI(src)
    if (data) {
      img.setAttribute('src', data)
    } else {
      missing++
      img.removeAttribute('src')
      img.setAttribute('alt', (img.getAttribute('alt') || '') + '（图片已丢失）')
    }
  }

  const html = wrap(title, meta, clone.innerHTML, collectStyles())
  return { html, bytes: new Blob([html]).size, images, missing }
}

/** 把同源资源抓成 data URI。抓不到就返回空——缺一张图不该让整次导出失败。 */
async function toDataURI(url: string): Promise<string> {
  try {
    const res = await fetch(url, { credentials: 'same-origin' })
    if (!res.ok) return ''
    const blob = await res.blob()
    return await new Promise<string>((resolve) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => resolve('')
      reader.readAsDataURL(blob)
    })
  } catch {
    return ''
  }
}

/**
 * 收集页面上的样式。
 *
 * 优先读 cssRules；同源的构建产物通常读得到。读不到时（个别浏览器对
 * 内联样式表的限制）退回一份最小可读样式，保证导出的东西至少能读，
 * 而不是变成一堆没有排版的裸文字。
 */
function collectStyles(): string {
  const parts: string[] = []
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      const rules = (sheet as CSSStyleSheet).cssRules
      if (!rules) continue
      for (const rule of Array.from(rules)) parts.push(rule.cssText)
    } catch {
      // 跨源样式表读不到规则，跳过即可
    }
  }
  return parts.length > 0 ? parts.join('\n') : FALLBACK_CSS
}

const FALLBACK_CSS = `
body{margin:0;padding:32px 20px;font:16px/1.85 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#1a1c1b;background:#fff}
.pe-export{max-width:42em;margin:0 auto}
img{max-width:100%;height:auto}
pre{overflow-x:auto;padding:12px;background:#f4f4f2;border-radius:4px}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.92em}
blockquote{margin:0;padding-left:14px;border-left:3px solid #ddd;color:#555}
table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:6px 10px}
`

function wrap(title: string, meta: string, body: string, css: string): string {
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHTML(title)}</title>
<style>
${css}
/* 导出件是独立文档，不是应用界面：去掉那些只在应用里有意义的布局。 */
body{margin:0;background:var(--surface,#fff)}
.pe-export{max-width:42em;margin:0 auto;padding:32px 20px 64px}
.pe-export-head{border-bottom:1px solid rgba(128,128,128,.25);padding-bottom:14px;margin-bottom:28px}
.pe-export-head h1{margin:0 0 6px}
.pe-export-meta{font-size:12.5px;opacity:.65}
.pe-export-foot{margin-top:48px;padding-top:14px;border-top:1px solid rgba(128,128,128,.25);font-size:12px;opacity:.55}
</style>
</head>
<body>
<div class="pe-export">
  <div class="pe-export-head">
    <h1>${escapeHTML(title)}</h1>
    <div class="pe-export-meta">${escapeHTML(meta)}</div>
  </div>
  <div class="prose">${body}</div>
  <div class="pe-export-foot">由 PreviewEverywhere 导出于 ${new Date().toLocaleString()}</div>
</div>
</body>
</html>`
}

function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string,
  )
}
