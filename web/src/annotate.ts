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

/**
 * 把一个 DOM 边界点换算成规范化文本里的字符下标。
 *
 * 难点在于边界点**不一定落在文本节点上**，而这正是它出过的 bug。
 * 「某个文本节点第 0 个字符之前」这个位置，在 DOM 里有好几种等价写法：
 * (文本节点, 0)、(它的父元素, 0)、(祖父元素, 该子节点的下标)。
 * WebKit 长按选词时会规范化成后两种。于是在
 *
 *     <p data-blk=…>… in <strong>Ubuntu 14.04</strong>, …</p>
 *
 * 里长按 Ubuntu（加粗段落的第一个词），拿到的起点是 (strong, 0) 或 (p, 1)。
 * 终点同理：选中最后一个词 14.04 时终点会是 (strong, 1)；就算老老实实给出
 * (文本节点, 12)，那个偏移也已经越过了该节点最后一个字符的下标。
 *
 * 原先只按 `nodes[i] === node && offsets[i] >= offset` 找，这两种都对不上，
 * 于是一律返回块尾。后果是两个都很难看出来的故障：
 *
 *   起点对不上 → start ≥ end → 读成「没有选中」，文字明明被标出来了却不弹气泡。
 *   终点对不上 → end = 块尾 → 气泡照常弹出，但存下来的高亮一路吃到段落结尾。
 *
 * 而长按同一段里中间的词落在文本节点内部，没有等价写法，一切正常——
 * 所以这个 bug 藏得住，只在「加粗段落的第一个 / 最后一个词」上现形。
 *
 * 所以这里不认节点，只认**文档顺序**：返回第一个位置不早于该边界点的字符。
 * 边界点落在文本节点内部时，结果与原先逐字相同。
 */
export function indexOfDOMPosition(index: BlockIndex, node: Node, offset: number): number {
  const probe = document.createRange()
  try {
    probe.setStart(node, offset)
    probe.setEnd(node, offset)
    // chars 是按文档顺序排出来的，所以可以二分。
    // comparePoint 以探针为参照：1 = 该点在其后，0 = 重合，-1 = 在其前。
    let lo = 0
    let hi = index.chars.length
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (probe.comparePoint(index.nodes[mid], index.offsets[mid]) >= 0) hi = mid
      else lo = mid + 1
    }
    return lo
  } catch {
    // 边界点压根不在这棵树里。截断在块尾，与原先的兜底一致。
    return index.chars.length
  }
}

/** 把规范化文本里的区间换算回 DOM Range。 */
export function rangeFromOffsets(index: BlockIndex, start: number, end: number): Range | null {
  if (start >= end || start < 0 || start >= index.chars.length) return null
  const clampedEnd = Math.min(end, index.chars.length)

  const range = document.createRange()
  range.setStart(index.nodes[start], index.offsets[start])

  // 终点取「最后一个字符之后」，而不是「下一个字符之前」。
  // 两者通常落在同一处，但下一个字符是空白折叠出来的空格时不同：
  // 那个空格自己没有位置，记的是**再下一个**字符的位置，于是按它取终点
  // 会把中间那个真空格也盖进去——高亮比选中的词宽出一个空格。
  const last = index.chars[clampedEnd - 1]
  if (last !== ' ') {
    range.setEnd(index.nodes[clampedEnd - 1], index.offsets[clampedEnd - 1] + last.length)
  } else if (clampedEnd < index.chars.length) {
    // 区间本身以空格收尾。这种偏移只可能来自历史数据（现在读选区时两端的
    // 空格已经排除在外了），没有更好的落点，维持原来的取法。
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

  // 起点容器可能是文本节点，也可能是元素（见 indexOfDOMPosition 的说明），
  // 少数情况下还可能是注释节点——后者没有 closest，直接转型会当场抛异常。
  const startEl =
    range.startContainer.nodeType === Node.ELEMENT_NODE
      ? (range.startContainer as Element)
      : range.startContainer.parentElement
  const block = startEl?.closest('[data-blk]')
  if (!block || !root.contains(block)) return null

  const index = buildBlockIndex(block)
  let start = indexOfDOMPosition(index, range.startContainer, range.startOffset)
  let end = block.contains(range.endContainer)
    ? indexOfDOMPosition(index, range.endContainer, range.endOffset)
    : index.chars.length // 选到了块外，截断在块尾
  if (end > index.chars.length) end = index.chars.length

  // 把两端的空格排除在偏移之外，让偏移和引文说的是同一段。
  //
  // 规范化文本里的空格记的是**后一个字符**的 DOM 位置——它是空白折叠出来的，
  // 自己没有位置。于是选中段落中间的一个词时，起点会落在这个空格上，
  // 偏移比引文多出一格。引文本身是收过边的，两者从此对不上。
  //
  // 服务端发现对不上就会拿引文去块里重找一遍，而它找的是**第一处**。
  // 后果：一段话里出现两次的词，选中后面那处，高亮会跑到前面那处去。
  // （实测：「We regenerate the build directory whenever the build directory
  // goes stale.」里选中第二个 the build directory，存下来的是第一个。）
  // 在这里对齐好，那条兜底路径就不必启动了——它本来就只该在文档被改写后启动。
  //
  // 规范化后的空白一律是单个空格，所以按空格收边与 trim 等价。
  while (start < end && index.chars[start] === ' ') start++
  while (end > start && index.chars[end - 1] === ' ') end--
  if (end <= start) return null

  const exact = index.chars.slice(start, end).join('')
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
