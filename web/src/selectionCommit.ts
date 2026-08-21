/**
 * 「选区变了没有」这件事，什么时候才算数。
 *
 * 背景：这个判断已经改坏过两次，两次的根子是同一个——代码在第一次读到
 * 非折叠选区时就急着把气泡显示出来，然后再想办法撤回。由此产生两种故障：
 *
 *   幽灵气泡        iOS 长按过程中会先产生一个临时选区、随后又把它收掉，
 *                   而收掉这一下不一定触发 selectionchange。急着显示的气泡
 *                   就指向了一个已经不存在的选区，且没有可靠信号来纠正。
 *   气泡与选中同归于尽
 *                   为了消灭幽灵气泡而加的「事后复核」，撞上了 iOS 弹出系统
 *                   菜单那段 getSelection() 读不到东西的空窗期，于是把活着的
 *                   选区连气泡一起拆了。
 *
 * 所以这里换一条规则，两个方向对称：**任何状态变化都要连续两次读数一致才算数**。
 *
 *   临时选区   读到 A → 读到空 → 读到空：两次空一致，提交「无选区」。
 *              气泡自始至终没出现过，不需要撤回，也就没有撤回带来的破坏。
 *   真实选区   读到 A → 读到 A：一致，提交。代价是气泡晚约一次确认间隔出现，
 *              而长按本身就要半秒，这点延迟感觉不到。
 *   假的读不到 已提交 A，读到空 → 与上次读数不一致 → 不提交，气泡原地不动；
 *              下一次又读到 A，两次一致，仍是 A。气泡从头到尾没有闪。
 *
 * 桌面端不需要这套：鼠标划词不会产生临时选区，那里的即时反馈更重要。
 */

/** 选区的身份。位置（rect）会随滚动变化，不参与身份判定。 */
export interface SelAnchor {
  blk: string
  startOff: number
  endOff: number
}

/** 两次读数之间的确认间隔。 */
export const CONFIRM_MS = 180

/**
 * 连续确认的次数上限。
 * 正常情况下一两次就稳定了；设上限是为了万一读数持续抖动时不至于
 * 无限自我调度下去——到点就按当前读数提交，行为有界。
 */
export const MAX_RECHECKS = 8

export function sameAnchor(a: SelAnchor | null, b: SelAnchor | null): boolean {
  if (a === null || b === null) return a === b
  return a.blk === b.blk && a.startOff === b.startOff && a.endOff === b.endOff
}

export type Step = { action: 'commit' } | { action: 'recheck'; delayMs: number }

/**
 * 这一次读数该不该算数。
 *
 * @param lastRead   上一次读到的选区（不是已提交的那个）
 * @param current    这一次读到的
 * @param rechecks   已经连续确认了多少次
 * @param needConfirm 是否需要确认。桌面端传 false，立即生效。
 */
export function nextStep(
  lastRead: SelAnchor | null,
  current: SelAnchor | null,
  rechecks: number,
  needConfirm: boolean,
): Step {
  if (!needConfirm) return { action: 'commit' }
  if (sameAnchor(lastRead, current)) return { action: 'commit' }
  if (rechecks >= MAX_RECHECKS) return { action: 'commit' }
  return { action: 'recheck', delayMs: CONFIRM_MS }
}
