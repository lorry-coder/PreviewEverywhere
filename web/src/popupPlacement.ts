/**
 * 浮层（划词气泡、批注卡片）放在哪里。
 *
 * 单独拆出来是因为这段判断有两条很难在开发机上复现的约束：
 *
 *  1. iOS Safari 长按选中文字后，系统会在选区紧邻处画出自己的编辑菜单
 *     （拷贝／查询／翻译）。那是系统层绘制的东西，网页既盖不住它，
 *     也收不到落在它上面的点击。于是「浮在选区正上方」这个在桌面端
 *     最自然的做法，在 iOS 上等于把按钮放到了点不到的位置。
 *  2. 手机屏就那么宽。选区靠近左右边缘时，一个居中对齐的气泡会有一半
 *     溢出到屏幕外——按钮还在，但看不见也点不着。
 *
 * 两条的解法是同一个：够窄或者是触屏，就别浮了，贴到视口边缘去。
 * 这个文件刻意不碰 DOM，好让这条逻辑能被单独跑起来验证。
 */

export interface Viewport {
  width: number
  height: number
}

export interface PlacementInput {
  /** 粗指针（触屏）。系统级选区菜单只在这类设备上抢位置。 */
  coarse: boolean
  /** 正在打字（软键盘会从底部升起）。 */
  composing: boolean
  /** 锚点矩形，视口坐标。 */
  anchorTop: number
  anchorBottom: number
  anchorLeft: number
  anchorWidth: number
  /** 浮层自身的宽度。用来把它夹在视口内，避免半个身子在屏幕外。 */
  popupWidth: number
  viewport: Viewport
}

export type Placement =
  | { mode: 'float'; top: number; left: number }
  | { mode: 'dock'; edge: 'top' | 'bottom' }

/** 锚点落在视口下方多少比例时，改贴顶部。 */
const DOCK_FLIP_RATIO = 0.55

/**
 * 窄到这个宽度以下就一律贴边。
 * 依据是气泡自身的 max-width（320）加上两边留白——再窄就没有「浮」的余地了。
 */
export const DOCK_MAX_WIDTH = 640

/** 浮动时距视口边缘至少留这么多，免得贴着边难点。 */
const EDGE_GAP = 8

export function shouldDock(coarse: boolean, viewportWidth: number): boolean {
  return coarse || viewportWidth < DOCK_MAX_WIDTH
}

export function placePopup(i: PlacementInput): Placement {
  if (shouldDock(i.coarse, i.viewport.width)) {
    // 贴哪一边取决于锚点在屏幕的哪半边——离锚点远的那一边才躲得开系统菜单。
    // 打字时一律贴顶：软键盘会把底部整个吃掉。
    const flip = i.composing || i.anchorBottom > i.viewport.height * DOCK_FLIP_RATIO
    return { mode: 'dock', edge: flip ? 'top' : 'bottom' }
  }

  // 桌面端浮在锚点上方。left 是浮层的中心（配合 translateX(-50%)），
  // 所以夹紧时要把自身宽度的一半算进去，否则「贴着视口右边」实际是
  // 右半个气泡已经在屏幕外了——这正是手机上「点不到疑问」的成因。
  const half = i.popupWidth / 2
  const center = i.anchorLeft + i.anchorWidth / 2
  const min = EDGE_GAP + half
  const max = i.viewport.width - EDGE_GAP - half
  // 浮层比视口还宽时 min 会大于 max，这时居中是唯一说得通的选择。
  const left = min > max ? i.viewport.width / 2 : Math.min(Math.max(center, min), max)

  return { mode: 'float', top: Math.max(EDGE_GAP, i.anchorTop - EDGE_GAP), left }
}
