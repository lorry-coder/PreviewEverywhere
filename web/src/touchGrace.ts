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
 * 下一次检查选区应当等多久。
 *
 * 处在宽限期内时把检查推迟到宽限期之后，而不是丢掉它——丢掉的话，
 * 那一次「选区没了」就永远没人再管了。
 */
export function checkDelay(now: number, until: number, min = 60): number {
  return Math.max(min, until - now)
}
