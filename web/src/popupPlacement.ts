/**
 * 划词气泡放在哪里。
 *
 * 单独拆出来是因为这段判断有一条很难在开发机上复现的约束：iOS Safari 长按
 * 选中文字后，系统会在选区紧邻处画出自己的编辑菜单（拷贝／查询／翻译）。
 * 那是系统层绘制的东西，网页既盖不住它，也收不到落在它上面的点击。
 * 于是「气泡贴在选区正上方」这个在桌面端最自然的做法，在 iOS 上等于把
 * 按钮放到了一个点不到的位置。
 *
 * 抢是抢不赢的，只能让开：触屏上把气泡贴到视口边缘——那里永远不是系统菜单
 * 会出现的地方。这个文件刻意不碰 DOM，好让这条逻辑能被单独跑起来验证。
 */

export interface Viewport {
  width: number
  height: number
}

export interface PlacementInput {
  /** 粗指针（触屏）。系统级选区菜单只在这类设备上抢位置。 */
  coarse: boolean
  /** 正在写批注正文（软键盘会从底部升起）。 */
  composing: boolean
  /** 选区矩形，视口坐标。 */
  selTop: number
  selBottom: number
  selLeft: number
  selWidth: number
  viewport: Viewport
}

export type Placement =
  | { mode: 'float'; top: number; left: number }
  | { mode: 'dock'; edge: 'top' | 'bottom' }

/** 选区落在视口下方多少比例时，改贴顶部。 */
const DOCK_FLIP_RATIO = 0.55

export function placePopup(i: PlacementInput): Placement {
  if (i.coarse) {
    // 贴哪一边取决于选区在屏幕的哪半边——离选区远的那一边才躲得开系统菜单。
    // 打字时一律贴顶：软键盘会把底部整个吃掉。
    const flip = i.composing || i.selBottom > i.viewport.height * DOCK_FLIP_RATIO
    return { mode: 'dock', edge: flip ? 'top' : 'bottom' }
  }
  // 桌面端没有这个冲突，气泡跟着选区走，但不能顶到视口外面去。
  return {
    mode: 'float',
    top: Math.max(8, i.selTop - 8),
    left: Math.min(Math.max(12, i.selLeft + i.selWidth / 2), i.viewport.width - 12),
  }
}
