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

/**
 * 单份导出的上限。
 *
 * data URI 会让体积比原始图片大约三分之一，再经 JSON 转义送到服务端还要再涨一点，
 * 所以这个数要明显低于服务端的接收上限（48MB），留够余量。
 * 另一个理由更实际：手机上把几十兆的字符串在内存里搬来搬去本身就吃不消，
 * 而这个平台的主场就是手机。
 */
export const MAX_EXPORT_BYTES = 24 * 1024 * 1024

/**
 * 导出成什么。
 *
 *   html —— 自包含 HTML，浏览器怎么显示就怎么带走
 *   pdf  —— 同一份内容，但要先绕开服务端 PDF 引擎的三处限制（见 toPDFFlavour）
 */
export type ExportFlavour = 'html' | 'pdf'

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
  flavour: ExportFlavour = 'html',
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

  if (flavour === 'pdf') await toPDFFlavour(clone)

  const html =
    flavour === 'pdf'
      ? wrapForPDF(title, meta, clone.innerHTML)
      : wrap(title, meta, clone.innerHTML, collectStyles())
  return { html, bytes: new Blob([html]).size, images, missing }
}

/**
 * 把正文改造成服务端 PDF 引擎吃得下的样子。
 *
 * 三处限制都是实测出来的，不是照抄文档：
 *
 *  1. <pre> 标签会被强制用 PDF 内置的等宽字体，而那是拉丁专用字体——
 *     代码块里的中文注释会整片变成 ???。换成 white-space: pre-wrap 的
 *     div 就正常了（同样的样式加在 div 上没有这个问题，只有标签本身有）。
 *  2. SVG 里的文字只能用 PDF 内置的 14 种标准字体，中文必然乱码。
 *     所以把每个 SVG 先在浏览器里光栅化成 PNG——mermaid 图表的中文标签
 *     因此得以保住，代价是图表文字不可选。
 *  3. 图片会被拉伸到超过原始尺寸，需要显式限住。
 */
async function toPDFFlavour(root: HTMLElement): Promise<void> {
  for (const pre of Array.from(root.querySelectorAll('pre'))) {
    const div = document.createElement('div')
    div.className = 'pe-code'
    div.textContent = pre.textContent ?? ''
    pre.replaceWith(div)
  }

  for (const svg of Array.from(root.querySelectorAll('svg'))) {
    const png = await svgToPNG(svg)
    if (!png) {
      // 转不出来就留个说明，好过塞一张中文乱码的图。
      const note = document.createElement('div')
      note.className = 'pe-code'
      note.textContent = '（此处有一张图表，导出 PDF 时未能转换）'
      svg.replaceWith(note)
      continue
    }
    const img = document.createElement('img')
    img.src = png.data
    img.setAttribute('width', String(png.width))
    svg.replaceWith(img)
  }
}

/**
 * 把一个 SVG 画成 PNG。
 *
 * 用 2 倍分辨率是因为 PDF 里会按 CSS 像素摆放，1 倍在纸上会糊。
 * 失败就返回 null——一张转不出来的图表不该让整次导出失败。
 */
async function svgToPNG(
  svg: SVGElement,
): Promise<{ data: string; width: number } | null> {
  try {
    const rect = svg.getBoundingClientRect()
    const w = Math.max(1, Math.round(rect.width || 320))
    const h = Math.max(1, Math.round(rect.height || 180))

    const clone = svg.cloneNode(true) as SVGElement
    clone.setAttribute('width', String(w))
    clone.setAttribute('height', String(h))
    if (!clone.getAttribute('xmlns')) {
      clone.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
    }
    const url =
      'data:image/svg+xml;charset=utf-8,' +
      encodeURIComponent(new XMLSerializer().serializeToString(clone))

    const img = new Image()
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve()
      img.onerror = () => reject(new Error('svg load'))
      img.src = url
    })

    const scale = 2
    const canvas = document.createElement('canvas')
    canvas.width = w * scale
    canvas.height = h * scale
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    // 白底：PDF 是白纸，透明背景在深色主题下导出会变成黑块。
    ctx.fillStyle = '#ffffff'
    ctx.fillRect(0, 0, canvas.width, canvas.height)
    ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
    return { data: canvas.toDataURL('image/png'), width: w }
  } catch {
    return null
  }
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

/**
 * PDF 专用的页面骨架。
 *
 * 刻意不套应用样式表：里面有自定义属性、color-mix()、媒体查询这类
 * 服务端 PDF 引擎消化不了的东西，硬塞进去只会得到一份排版错乱的文件。
 * 这里写一套明确、克制、只用基础属性的样式。
 */
function wrapForPDF(title: string, meta: string, body: string): string {
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>${escapeHTML(title)}</title>
<style>
@page { size: A4; margin: 18mm 16mm; }
body { font-size: 10.5pt; line-height: 1.75; color: #1a1c1b; }
h1 { font-size: 19pt; margin: 0 0 4px; }
h2 { font-size: 14pt; margin: 20px 0 8px; }
h3 { font-size: 12pt; margin: 16px 0 6px; }
p, li { margin: 0 0 10px; }
ul, ol { padding-left: 22px; }
a { color: #1a5fb4; }
img { max-width: 100%; height: auto; }
blockquote { margin: 0 0 12px; padding-left: 12px; border-left: 3px solid #c08a22; color: #555; }
table { border-collapse: collapse; width: 100%; font-size: 9.5pt; margin-bottom: 12px; }
th, td { border: 1px solid #ddd; padding: 6px 9px; text-align: left; }
th { background: #f4f4f2; }
code { font-size: 9.5pt; }
/* 代码块用 div 而不是 pre：pre 标签会被强制用拉丁专用的内置等宽字体，
   里面的中文注释会整片变成 ???。 */
.pe-code { background: #f4f4f2; padding: 10px 12px; margin: 0 0 12px;
  white-space: pre-wrap; font-size: 9pt; line-height: 1.6; }
.pe-head { border-bottom: 1px solid #ddd; padding-bottom: 10px; margin-bottom: 22px; }
.pe-meta { font-size: 8.5pt; color: #777; }
</style>
</head>
<body>
<div class="pe-head">
  <h1>${escapeHTML(title)}</h1>
  <div class="pe-meta">${escapeHTML(meta)}</div>
</div>
${body}
</body>
</html>`
}

function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string,
  )
}
