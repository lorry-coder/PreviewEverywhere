/**
 * 选区事件记录器（全局）。
 *
 * 为什么要做成全局而不是只在自查页里：问题发生在**阅读页**——长按选中之后
 * 气泡和选中标记互相打架。只在自查页录，录到的是一个没有气泡的环境，
 * 那正好把要观察的东西排除在外了。
 *
 * 现在的用法是：在自查页打开开关，去阅读页长按，再回自查页看记录。
 * 开关存在 sessionStorage 里，所以跨页面还在，关掉标签就没了。
 */

export interface TraceEntry {
  t: number
  ev: string
  ranges: number
  /** anchorNode 是否存在。iOS 点按钮时发的那种「什么也没说明」的事件，这里是 false。 */
  anchor: boolean
  collapsed: boolean
  len: number
  /** 划词气泡在不在。判断「气泡和选中互相打架」全靠这一列。 */
  bubble: boolean
}

const KEY = 'pe:trace'
const MAX = 80

/**
 * 记哪些事件。
 *
 * 指针与触摸两套都记：iOS 长按选词时系统会中途接管手势并发出 cancel，
 * 之后未必还有 up/end——这两套事件的实际先后，是判断「什么时候算手势结束」
 * 的唯一依据，而这件事在开发机上复现不了。
 */
const WATCHED = [
  'selectionchange',
  'pointerdown',
  'pointerup',
  'pointercancel',
  'touchstart',
  'touchend',
  'touchcancel',
]

let log: TraceEntry[] = []
let startedAt = 0
let listeners: (() => void)[] = []
let stop: (() => void) | null = null

export function traceEnabled(): boolean {
  try {
    return sessionStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}

export function setTraceEnabled(on: boolean) {
  try {
    if (on) sessionStorage.setItem(KEY, '1')
    else sessionStorage.removeItem(KEY)
  } catch {
    /* 隐私模式下写不进去，那就只在本页有效 */
  }
  if (on) {
    log = []
    startedAt = Date.now()
    attach()
  } else {
    detach()
  }
  emit()
}

export function traceLog(): TraceEntry[] {
  return log
}

export function subscribeTrace(fn: () => void): () => void {
  listeners.push(fn)
  return () => {
    listeners = listeners.filter((f) => f !== fn)
  }
}

function emit() {
  for (const fn of listeners) fn()
}

function push(ev: string) {
  const s = window.getSelection()
  log = [
    ...log,
    {
      t: Date.now() - startedAt,
      ev,
      ranges: s?.rangeCount ?? 0,
      anchor: !!s?.anchorNode,
      collapsed: s?.isCollapsed ?? true,
      len: (s?.toString() ?? '').length,
      bubble: !!document.querySelector('.sel-popup'),
    },
  ].slice(-MAX)
  emit()
}

function attach() {
  detach()
  const handlers = WATCHED.map((name) => {
    const fn = () => push(name)
    // 用捕获阶段：即使有人在冒泡途中把事件拦了，记录也不会漏。
    document.addEventListener(name, fn, true)
    return [name, fn] as const
  })

  // 盯住正文的 DOM 变动。
  //
  // 「选中标记自己消失了」最可能的成因，就是有人在手势进行中改了正文 DOM——
  // iOS 会因此丢掉选区。光看事件序列分不出「选区自己没的」和「被我们改没的」，
  // 加上这一条才分得出来。
  let observer: MutationObserver | null = null
  const watchProse = () => {
    const prose = document.querySelector('.prose')
    if (!prose || observer) return
    observer = new MutationObserver((records) => {
      const n = records.reduce(
        (sum, r) => sum + r.addedNodes.length + r.removedNodes.length,
        0,
      )
      if (n > 0) push(`正文 DOM 变动 ×${n}`)
    })
    observer.observe(prose, { childList: true, subtree: true, characterData: true })
  }
  watchProse()
  // 阅读页是异步渲染出来的，等它出现再挂上。
  const poll = window.setInterval(watchProse, 500)

  stop = () => {
    for (const [name, fn] of handlers) document.removeEventListener(name, fn, true)
    observer?.disconnect()
    observer = null
    window.clearInterval(poll)
  }
}

function detach() {
  stop?.()
  stop = null
}

/** 页面加载时若开关还开着，就接着记。 */
export function resumeTrace() {
  if (traceEnabled()) {
    if (startedAt === 0) startedAt = Date.now()
    attach()
  }
}

export function traceAsText(): string {
  return log
    .map(
      (e) =>
        `${e.t}ms ${e.ev} ranges=${e.ranges} anchor=${e.anchor} ` +
        `collapsed=${e.collapsed} len=${e.len} bubble=${e.bubble}`,
    )
    .join('\n')
}
