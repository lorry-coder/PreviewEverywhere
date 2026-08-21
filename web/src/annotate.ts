/**
 * 浏览器选区与服务端字符偏移之间的换算。
 *
 * 服务端存的是「规范化后的纯文本」：空白折叠成单个空格、零宽字符剔除、
 * 两个汉字之间的换行不产生空格。DOM 里的 textContent 是原始文本，两者对不上。
 * 这个模块在两边之间建一张逐字符的映射表。
 */

import { classify, type SelectionState } from './selectionRead'
import { SPACE_RE, ZERO_WIDTH, isCJK } from './normalize'

export { normalize, isCJK } from './normalize'

/**
 * BlockIndex 把一个块的规范化文本和它在 DOM 里的位置对应起来。
 * chars[i] 这个字符位于 nodes[i] 这个文本节点的 offsets[i] 处。
 */
export interface BlockIndex {
  text: string
  chars: string[]
  nodes: Text[]
  offsets: number[]
}

export function buildBlockIndex(el: Element): BlockIndex {
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
  const chars: string[] = []
  const nodes: Text[] = []
  const offsets: number[] = []
  let pendingSpace = false
  let last = ''

  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const node = n as Text
    const data = node.data
    let at = 0
    for (const ch of data) {
      const start = at
      at += ch.length // 码点可能占两个 UTF-16 单元
      if (ZERO_WIDTH.has(ch)) continue
      if (SPACE_RE.test(ch)) {
        pendingSpace = true
        continue
      }
      if (pendingSpace && chars.length > 0 && !(isCJK(last) && isCJK(ch))) {
        chars.push(' ')
        nodes.push(node)
        offsets.push(start)
      }
      pendingSpace = false
      chars.push(ch)
      nodes.push(node)
      offsets.push(start)
      last = ch
    }
  }
  return { text: chars.join(''), chars, nodes, offsets }
}

/** 把 DOM 位置换算成规范化文本里的字符下标。 */
export function indexOfDOMPosition(index: BlockIndex, node: Node, offset: number): number {
  for (let i = 0; i < index.nodes.length; i++) {
    if (index.nodes[i] === node && index.offsets[i] >= offset) return i
  }
  // 不在本块内，或落在块尾之后
  return index.chars.length
}

/** 把规范化文本里的区间换算回 DOM Range。 */
export function rangeFromOffsets(index: BlockIndex, start: number, end: number): Range | null {
  if (start >= end || start < 0 || start >= index.chars.length) return null
  const clampedEnd = Math.min(end, index.chars.length)

  const range = document.createRange()
  range.setStart(index.nodes[start], index.offsets[start])
  if (clampedEnd < index.chars.length) {
    range.setEnd(index.nodes[clampedEnd], index.offsets[clampedEnd])
  } else {
    const lastNode = index.nodes[index.nodes.length - 1]
    range.setEnd(lastNode, lastNode.data.length)
  }
  return range
}

export interface Selected {
  blk: string
  startOff: number
  endOff: number
  exact: string
  /** 选区在视口里的位置，用来放弹出气泡。 */
  rect: DOMRect
}

/**
 * 读取当前选区，换算成「哪个块 + 块内起止偏移」。
 *
 * 跨块的选区会被截到起始块的末尾：批注模型里一条批注只归属一个块。
 * 这是个真实的限制，好在绝大多数划线都发生在一段之内。
 */
/** 一次读数的完整结论：状态 + （有选区时的）内容。 */
export interface SelectionRead {
  state: SelectionState
  value: Selected | null
}

/**
 * 读一次选区，并明确区分「读不出来」和「确实没有选中」。
 * 为什么必须分开，见 selectionRead.ts —— 这是这块反复出问题的根子。
 */
export function readSelectionState(root: HTMLElement): SelectionRead {
  const sel = window.getSelection()
  if (!sel) return { state: 'unknown', value: null }

  const facts = {
    rangeCount: sel.rangeCount,
    hasAnchorNode: sel.anchorNode !== null,
    isCollapsed: sel.isCollapsed,
    // 后两项要拿到 range 才知道，先按「满足」填，下面用真实结果覆盖。
    insideRoot: true,
    hasText: true,
  }
  if (classify(facts) === 'unknown') return { state: 'unknown', value: null }

  const value = readSelection(root)
  if (value) return { state: 'selection', value }

  // 有 anchorNode、也不是 unknown，但取不到正文里的有效选区：
  // 说明选区确实折叠了、或者落在正文之外，两者都该收起气泡。
  return { state: 'empty', value: null }
}

export function readSelection(root: HTMLElement): Selected | null {
  const sel = window.getSelection()
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null

  const range = sel.getRangeAt(0)
  if (!root.contains(range.startContainer)) return null

  const block = (range.startContainer.nodeType === Node.TEXT_NODE
    ? range.startContainer.parentElement
    : (range.startContainer as Element)
  )?.closest('[data-blk]')
  if (!block || !root.contains(block)) return null

  const index = buildBlockIndex(block)
  const start = indexOfDOMPosition(index, range.startContainer, range.startOffset)
  let end = block.contains(range.endContainer)
    ? indexOfDOMPosition(index, range.endContainer, range.endOffset)
    : index.chars.length // 选到了块外，截断在块尾

  if (end <= start) return null
  if (end > index.chars.length) end = index.chars.length

  const exact = index.chars.slice(start, end).join('').trim()
  if (!exact) return null

  return {
    blk: block.getAttribute('data-blk') || '',
    startOff: start,
    endOff: end,
    exact,
    rect: range.getBoundingClientRect(),
  }
}

/** 某条批注在页面上占据的矩形区域，用于画高亮层。 */
export function rectsForAnnotation(
  root: HTMLElement,
  blk: string,
  startOff: number,
  endOff: number,
): DOMRect[] {
  const block = root.querySelector(`[data-blk="${CSS.escape(blk)}"]`)
  if (!block) return []
  const index = buildBlockIndex(block)
  const range = rangeFromOffsets(index, startOff, endOff)
  if (!range) return []
  return Array.from(range.getClientRects())
}
