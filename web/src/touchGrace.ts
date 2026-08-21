/**
 * 手指落在气泡上时的「宽限期」。
 *
 * 为什么需要：iOS 上点按会先把选区收掉，再派发点击。若在这一瞬间跟着
 * 清空已捕获的选区，气泡会先消失，按钮就等于按不动。
 *
 * 为什么是时间戳而不是布尔量：布尔量要靠 touchend 来清，而 touchend
 * 不保证会来——手指从气泡上滑开、被滚动打断、touchcancel，任一情况都会
 * 让它永远停在 true。此后所有「选区没了」的判断全被跳过，气泡就永久
 * 赖在屏幕上，指向一个早已不存在的选区。这正是「屏幕上没有选中标记、
 * 气泡却弹出来了」那个偶发现象的成因之一。
 *
 * 时间戳到点自动失效，卡不住——这条不变量由 parity.sh 钉着。
 */

/** 手指按下后的宽限上限。取足够长以覆盖一次正常点按，但必须有限。 */
export const GRACE_AFTER_START = 1200
/** 手指抬起后的宽限。只需跨过「收选区 → 派发 click」这一小段。 */
export const GRACE_AFTER_END = 300

export function graceOnStart(now: number): number {
  return now + GRACE_AFTER_START
}

export function graceOnEnd(now: number): number {
  return now + GRACE_AFTER_END
}

export function inGrace(now: number, until: number): boolean {
  return now < until
}

/**
 * 选区刚建立后的静默期：这段时间里只接受「有选区」的读数。
 *
 * 「读不到选区」在 iOS 上并不等于「用户的选中没了」。系统自己的选区菜单
 * 弹出的那段时间里 getSelection() 会短暂读不到东西，此时若把气泡拆掉，
 * 随之而来的 DOM 变化还会让 iOS 把选中标记一起丢掉——表现就是
 * 「气泡和选中的文字一起消失」，比原来的幽灵气泡糟糕得多。
 *
 * 所以这里的取舍是明确的：宁可偶尔多留一会儿气泡，也不能拆掉活着的选区。
 *
 * 取值要在两个方向之间平衡：太短挡不住那段空窗期；太长的话「点别处收起气泡」
 * 会在这段时间里失灵，用起来像卡住了。400ms 足够覆盖 iOS 弹出菜单的那一下，
 * 又短到人不会在这期间去点别处收气泡。
 */
export const SETTLE_MS = 400

/**
 * 下一次检查选区应当等多久。
 *
 * 处在宽限期内时把检查推迟到宽限期之后，而不是丢掉它——丢掉的话，
 * 那一次「选区没了」就永远没人再管了。
 */
export function checkDelay(now: number, until: number, min = 60): number {
  return Math.max(min, until - now)
}
