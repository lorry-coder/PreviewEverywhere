/**
 * 浮层（划词气泡、批注卡片）放在哪里。
 *
 * 形态是小气泡，贴着选区走——它比贴屏幕边缘的整条操作栏轻快得多。
 * 但要让气泡在手机上真的可用，得同时躲开三样东西，而这三样在开发机上
 * 一个都复现不了：
 *
 *  1. iOS 的系统选区菜单（拷贝／查询／翻译）。它由系统画在网页之上，
 *     我们既盖不住也收不到落在它上面的点击。它默认出现在选区**上方**，
 *     上方放不下时翻到下方。所以气泡的策略是：永远走另一侧；
 *     被迫同侧时，把菜单的高度让出来。
 *  2. Safari 的地址栏。iOS 15 起默认在屏幕底部，而 position: fixed 是相对
 *     **布局**视口定位的，所以「屏幕底部」和「可见区域底部」不是一回事。
 *     调用方传进来的 view 必须是可见区域（visualViewport），不是 innerHeight。
 *  3. 屏幕左右边缘。居中对齐的气泡在选区靠边时会有一半溢出到屏幕外，
 *     按钮还在，但看不见也点不着。
 *
 * 这个文件刻意不碰 DOM，好让上面这些规则能被单独跑起来验证——
 * 它们错了的时候，桌面端往往一切正常，只有手机会坏。
 */

/** 可见区域，视口坐标。top/bottom 来自 visualViewport，不是 innerHeight。 */
export interface VisibleArea {
  top: number
  bottom: number
  width: number
}

export interface PlacementInput {
  /** 锚点矩形（选区或高亮），视口坐标。 */
  anchorTop: number
  anchorBottom: number
  anchorLeft: number
  anchorWidth: number
  /** 浮层自身尺寸。夹紧必须知道它，否则「贴着右边」其实是半个身子在屏幕外。 */
  popupWidth: number
  popupHeight: number
  view: VisibleArea
  /** 触屏：要给系统的选区菜单让出位置。桌面端没有这回事。 */
  avoidSystemMenu: boolean
}

export interface Placement {
  left: number
  top: number
  /** 落在锚点的哪一侧，供箭头之类的装饰使用。 */
  side: 'above' | 'below'
}

/** 气泡与锚点之间的间隙。 */
const ANCHOR_GAP = 10
/** 与可见区域边缘之间至少留这么多。 */
const EDGE_GAP = 8
/**
 * 给 iOS 系统菜单让出的高度。
 * 实测那个菜单连同它与选区之间的留白大约 44–52pt，取 56 留一点余量——
 * 宁可多让一点显得松，也不能让按钮压在一个点不到的东西下面。
 */
const SYSTEM_MENU_H = 56

export function placePopup(i: PlacementInput): Placement {
  const { view } = i
  const spaceAbove = i.anchorTop - view.top

  // 系统菜单默认贴在选区上方，上方塞不下才翻到下方。
  const menuAbove = i.avoidSystemMenu && spaceAbove >= SYSTEM_MENU_H
  const menuBelow = i.avoidSystemMenu && !menuAbove

  let side: 'above' | 'below'
  let top: number

  if (i.avoidSystemMenu) {
    // 触屏一律先试下方：菜单在上方时那里是干净的；菜单也在下方时，
    // 就排在菜单后面，而不是跟它抢同一块地方。
    side = 'below'
    top = i.anchorBottom + ANCHOR_GAP + (menuBelow ? SYSTEM_MENU_H : 0)

    if (top + i.popupHeight > view.bottom - EDGE_GAP) {
      // 下方放不下就翻上去。菜单在上方的话同样要让开它。
      const above = i.anchorTop - ANCHOR_GAP - i.popupHeight - (menuAbove ? SYSTEM_MENU_H : 0)
      if (above >= view.top + EDGE_GAP) {
        side = 'above'
        top = above
      }
    }
  } else {
    // 桌面端没有系统菜单来抢位置，保持原来的手感：气泡浮在选区上方。
    side = 'above'
    top = i.anchorTop - ANCHOR_GAP - i.popupHeight
    if (top < view.top + EDGE_GAP) {
      side = 'below'
      top = i.anchorBottom + ANCHOR_GAP
    }
  }

  // 无论上面怎么选，最后都必须落在可见区域内。放不下时贴住上沿——
  // 露出顶部（按钮在那里）比露出底部有用。
  const maxTop = view.bottom - EDGE_GAP - i.popupHeight
  const minTop = view.top + EDGE_GAP
  top = maxTop < minTop ? minTop : Math.min(Math.max(top, minTop), maxTop)

  return { left: clampLeft(i), top, side }
}

/** 水平方向：以锚点中心对齐，但整体夹在可见区域内。 */
function clampLeft(i: PlacementInput): number {
  const center = i.anchorLeft + i.anchorWidth / 2
  const min = EDGE_GAP
  const max = i.view.width - EDGE_GAP - i.popupWidth
  // 气泡比屏幕还宽时 min 会大于 max，这时贴左边是唯一说得通的选择。
  if (min > max) return min
  return Math.min(Math.max(center - i.popupWidth / 2, min), max)
}
