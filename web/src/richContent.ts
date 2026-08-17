/**
 * 图表与公式的客户端渲染。这两样只有 JS 实现，服务端画不了。
 *
 * 最关键的约束是：渲染不能破坏批注偏移。
 *
 * 服务端存的偏移是按规范化纯文本算的，而 buildBlockIndex 走的是 DOM 文本节点。
 * 如果把 mermaid 源码换成 SVG、把 $E=mc^2$ 换成排版结果，这一段的文本就变了，
 * 落在同一段里的批注会整体错位。
 *
 * 解法是把原始源码留在 DOM 里，用 display:none 藏起来：
 * display:none 的文本仍然计入 textContent、仍然被 TreeWalker 遍历到，
 * 但 getClientRects 不会返回矩形，高亮层自然会跳过它。
 * 于是偏移不变，画面也干净。
 */

/** 藏起源码但保留文本的包装元素。 */
function hideSource(nodes: Node[]): HTMLElement {
  const holder = document.createElement('span')
  holder.className = 'pe-src'
  for (const n of nodes) holder.appendChild(n)
  return holder
}

function prefersDark(): boolean {
  const explicit = document.documentElement.getAttribute('data-theme')
  if (explicit === 'dark') return true
  if (explicit === 'light') return false
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
}

/** 渲染正文里的图表与公式。失败不抛，坏掉的那一块保留源码即可。 */
export async function renderRichContent(root: HTMLElement): Promise<void> {
  await Promise.all([renderMermaid(root), renderMath(root)])
}

// ── mermaid ───────────────────────────────────────────────────────

async function renderMermaid(root: HTMLElement): Promise<void> {
  const blocks = Array.from(
    root.querySelectorAll<HTMLElement>('pre.mermaid:not([data-pe-rendered])'),
  )
  if (blocks.length === 0) return

  let mermaid
  try {
    // 按需加载：mermaid 打包出来一两兆，不能让没有图的文档也等它。
    mermaid = (await import('mermaid')).default
  } catch (err) {
    // 静默失败最难查：页面上是一段看不懂的源码，控制台什么都没有。
    console.warn('[pe] mermaid 加载失败', err)
    for (const el of blocks) el.setAttribute('data-pe-error', '1')
    return
  }
  mermaid.initialize({
    startOnLoad: false,
    theme: prefersDark() ? 'dark' : 'neutral',
    securityLevel: 'strict',
    fontFamily: 'inherit',
  })

  for (const [i, el] of blocks.entries()) {
    const source = el.textContent ?? ''
    if (!source.trim()) continue
    el.setAttribute('data-pe-rendered', '1')
    try {
      const { svg } = await mermaid.render(`pe-mermaid-${Date.now()}-${i}`, source)
      const src = hideSource(Array.from(el.childNodes))
      const out = document.createElement('div')
      out.className = 'pe-diagram'
      out.innerHTML = svg
      el.replaceChildren(src, out)
    } catch (err) {
      // 图画不出来就把源码露出来，总比给一片空白强。
      console.warn('[pe] mermaid 渲染失败', err)
      el.setAttribute('data-pe-error', '1')
    }
  }
}

// ── KaTeX ─────────────────────────────────────────────────────────

const DISPLAY_MATH = /\$\$([\s\S]+?)\$\$/g
const INLINE_MATH = /\$([^$\n]+?)\$/g

/**
 * 行内公式的误判风险很高：「$100 和 $200」会被当成一段公式。
 * 所以行内模式额外要求内容里出现 TeX 特征字符，纯数字和文字一律放过。
 */
function looksLikeTeX(s: string): boolean {
  return /[\\^_{}=]/.test(s)
}

async function renderMath(root: HTMLElement): Promise<void> {
  const targets = collectMathTextNodes(root)
  if (targets.length === 0) return

  let katex
  try {
    katex = (await import('katex')).default
    await import('katex/dist/katex.min.css')
  } catch (err) {
    console.warn('[pe] KaTeX 加载失败', err)
    return
  }

  for (const node of targets) {
    const parts = splitMath(node.data)
    // 判据是「有没有找到公式」，不是「拆出了几段」——
    // 整个文本节点本身就是一段公式时只会拆出 1 个元素，
    // 按长度判断会把这种最常见的块级公式整个跳过。
    if (!parts.some((p) => typeof p !== 'string')) continue

    const frag = document.createDocumentFragment()
    for (const part of parts) {
      if (typeof part === 'string') {
        frag.appendChild(document.createTextNode(part))
        continue
      }
      const wrap = document.createElement('span')
      wrap.className = part.display ? 'pe-math display' : 'pe-math'
      // 源码留在 DOM 里（藏起来），批注偏移才不会因为渲染而漂移。
      wrap.appendChild(hideSource([document.createTextNode(part.raw)]))

      const out = document.createElement('span')
      out.className = 'pe-math-out'
      out.setAttribute('aria-hidden', 'true')
      try {
        katex.render(part.tex, out, { displayMode: part.display, throwOnError: false })
        wrap.appendChild(out)
      } catch {
        // 渲染不了就把源码放回可见状态
        wrap.replaceChildren(document.createTextNode(part.raw))
      }
      frag.appendChild(wrap)
    }
    node.replaceWith(frag)
  }
}

function collectMathTextNodes(root: HTMLElement): Text[] {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const out: Text[] = []
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const t = n as Text
    if (!t.data.includes('$')) continue
    // 代码块里的 $ 是 shell 提示符，不是公式。
    if (t.parentElement?.closest('pre, code, .pe-src, .pe-math')) continue
    out.push(t)
  }
  return out
}

interface MathPart {
  raw: string
  tex: string
  display: boolean
}

/**
 * 把一段文本拆成「普通文本」与「公式」交替的序列。
 *
 * 导出是为了能被测试直接覆盖：这里出过一个不容易看出来的 bug——
 * 用「拆出几段」判断有没有公式，会把「整个文本节点就是一段块级公式」
 * 这种最常见的情况整个跳过，页面上就只剩 $$...$$ 源码。
 */
export function splitMath(text: string): (string | MathPart)[] {
  const found: { start: number; end: number; part: MathPart }[] = []

  for (const m of text.matchAll(DISPLAY_MATH)) {
    if (m.index === undefined) continue
    found.push({
      start: m.index,
      end: m.index + m[0].length,
      part: { raw: m[0], tex: m[1].trim(), display: true },
    })
  }
  for (const m of text.matchAll(INLINE_MATH)) {
    if (m.index === undefined) continue
    const inner = m[1]
    if (!inner.trim() || !looksLikeTeX(inner)) continue
    // 已经被块级公式覆盖的位置不再重复匹配
    if (found.some((f) => m.index! >= f.start && m.index! < f.end)) continue
    found.push({
      start: m.index,
      end: m.index + m[0].length,
      part: { raw: m[0], tex: inner.trim(), display: false },
    })
  }
  if (found.length === 0) return [text]

  found.sort((a, b) => a.start - b.start)
  const out: (string | MathPart)[] = []
  let at = 0
  for (const f of found) {
    if (f.start < at) continue
    if (f.start > at) out.push(text.slice(at, f.start))
    out.push(f.part)
    at = f.end
  }
  if (at < text.length) out.push(text.slice(at))
  return out
}
