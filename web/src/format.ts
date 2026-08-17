/** 相对时间。读 agent 产出时，「2 分钟前」比绝对时间戳有用得多。 */
export function relativeTime(unixSeconds: number): string {
  const diff = Date.now() / 1000 - unixSeconds
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  if (diff < 86400 * 2) return '昨天'
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)} 天前`

  const d = new Date(unixSeconds * 1000)
  const sameYear = d.getFullYear() === new Date().getFullYear()
  const md = `${d.getMonth() + 1} 月 ${d.getDate()} 日`
  return sameYear ? md : `${d.getFullYear()} 年 ${md}`
}

/**
 * 预计阅读时长。中文按每分钟 450 字算，比英文的 200 词慢一档——
 * 这些文档是要「仔细研读」的，不是扫读。
 */
export function readingTime(chars: number): string {
  const minutes = Math.max(1, Math.round(chars / 450))
  return `约 ${minutes} 分钟`
}

/**
 * 在中西文边界插入视觉间隔，也就是语雀观感的关键细节之一。
 *
 * 关键点：插入的是空的 <span>，靠 margin 撑开，而不是真的空格字符。
 * 这样 DOM 的 textContent 与服务端存的纯文本仍然逐字符一致——
 * P3 的批注锚定要靠这个一致性把选区映射回存储偏移。
 */
const CJK = '\\u2e80-\\u2eff\\u2f00-\\u2fdf\\u3040-\\u309f\\u30a0-\\u30ff\\u3400-\\u4dbf\\u4e00-\\u9fff\\uf900-\\ufaff'
const LATIN = 'A-Za-z0-9@&=\\[\\]\\(\\)<>$%^*\\-+\\\\|/'
const CJK_THEN_LATIN = new RegExp(`([${CJK}])([${LATIN}])`, 'g')
const LATIN_THEN_CJK = new RegExp(`([${LATIN}])([${CJK}])`, 'g')

export function spaceCJK(root: HTMLElement) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const targets: Text[] = []
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const parent = (n as Text).parentElement
    // 代码块、图表源码、公式里的空隙会改变含义，跳过。
    if (parent?.closest('pre, code, .mermaid, .pe-math, .pe-src')) continue
    if ((n as Text).data.trim()) targets.push(n as Text)
  }

  for (const node of targets) {
    const text = node.data
    if (!CJK_THEN_LATIN.test(text) && !LATIN_THEN_CJK.test(text)) {
      CJK_THEN_LATIN.lastIndex = 0
      LATIN_THEN_CJK.lastIndex = 0
      continue
    }
    CJK_THEN_LATIN.lastIndex = 0
    LATIN_THEN_CJK.lastIndex = 0

    const boundaries: number[] = []
    for (let i = 1; i < text.length; i++) {
      const pair = text[i - 1] + text[i]
      CJK_THEN_LATIN.lastIndex = 0
      LATIN_THEN_CJK.lastIndex = 0
      if (CJK_THEN_LATIN.test(pair) || LATIN_THEN_CJK.test(pair)) boundaries.push(i)
    }
    if (!boundaries.length) continue

    const frag = document.createDocumentFragment()
    let last = 0
    for (const at of boundaries) {
      frag.appendChild(document.createTextNode(text.slice(last, at)))
      const spacer = document.createElement('span')
      spacer.className = 'pg'
      frag.appendChild(spacer)
      last = at
    }
    frag.appendChild(document.createTextNode(text.slice(last)))
    node.replaceWith(frag)
  }
}
