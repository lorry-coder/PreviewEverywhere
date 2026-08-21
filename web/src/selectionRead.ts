/**
 * 一次选区读数到底说明了什么。
 *
 * 这块反复改了五轮，根子始终是同一个误判：把「读不出选区」当成
 * 「用户取消了选中」。这两件事在 iOS 上完全不同——
 *
 *   在 iOS 上，用户点击按钮时会触发 selectionchange，但生成的选区是空的、
 *   anchorNode 为 null。它既不能说明选区在不在正文里，也不能说明之前的
 *   选中被取消了，因此必须直接退出，不能据此下任何结论。
 *   （见 recogito/text-annotator-js 的 selection-handler，注释与本条一致：
 *    https://github.com/recogito/text-annotator-js/pull/164#issuecomment-2416961473）
 *
 * 而我们的划词气泡恰恰全是按钮。用户每点一次「高亮」，iOS 就发一次这样的
 * 事件；把它读成「选区没了」，气泡就会在点击生效之前自己消失。
 * 之前那些宽限期、静默期、两次确认，全是在给这一个误判打补丁。
 *
 * 所以读数分三种，而不是「有」和「没有」两种：
 *
 *   selection  正文里有一段有效选区
 *   empty      确实没有选中了（选区已折叠，或落在正文之外）
 *   unknown    读不出来，不可据此下结论 —— 什么都不做
 */

export type SelectionState = 'selection' | 'empty' | 'unknown'

/** 判定所需的事实。全部由调用方从 DOM 里取，这里不碰 DOM，好单独验证。 */
export interface SelectionFacts {
  /** Selection.rangeCount */
  rangeCount: number
  /** Selection.anchorNode 是否存在。iOS 的垃圾事件里它是 null。 */
  hasAnchorNode: boolean
  /** Selection.isCollapsed */
  isCollapsed: boolean
  /** 选区起点是否落在正文容器内 */
  insideRoot: boolean
  /** 规范化之后是否还剩下文字 */
  hasText: boolean
}

export function classify(f: SelectionFacts): SelectionState {
  // 关键的一条：没有 anchorNode 或压根没有 range，说明这次事件什么也没告诉我们。
  // 它不等于「用户取消了选中」——iOS 点按钮时发的就是这种。
  if (f.rangeCount === 0 || !f.hasAnchorNode) return 'unknown'

  // 下面这些都是确凿的「现在没有可用选区」，可以据此收起气泡。
  if (f.isCollapsed) return 'empty'
  if (!f.insideRoot) return 'empty'
  if (!f.hasText) return 'empty'

  return 'selection'
}
