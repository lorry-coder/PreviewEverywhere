import { useEffect, useState } from 'react'

export interface ViewportInsets {
  /** 可见区域顶端相对布局视口的偏移。 */
  top: number
  /** 布局视口底端到可见区域底端的距离。软键盘或浏览器底栏占掉的就是它。 */
  bottom: number
  /** 可见区域高度。 */
  height: number
}

/**
 * 可见视口（visual viewport）相对布局视口的偏移。
 *
 * 为什么需要它：position: fixed 在 iOS 上是相对**布局**视口定位的，
 * 而实际能看见的是**可见**视口。两者在三种情况下不一致：
 *
 *   - iOS 15 起 Safari 的地址栏默认在屏幕底部，bottom: 0 会被它压住；
 *   - 软键盘升起时，fixed 元素会整个躲到键盘后面；
 *   - 页面被双指放大时。
 *
 * 只靠 env(safe-area-inset-bottom) 解决不了——那是刘海和手势条的尺寸，
 * 跟浏览器自己的工具栏没有关系。
 */
export function useVisualViewport(): ViewportInsets {
  const [insets, setInsets] = useState<ViewportInsets>(() => read())

  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const update = () => setInsets(read())
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    window.addEventListener('orientationchange', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
      window.removeEventListener('orientationchange', update)
    }
  }, [])

  return insets
}

function read(): ViewportInsets {
  const vv = window.visualViewport
  if (!vv) return { top: 0, bottom: 0, height: window.innerHeight }
  // 负值没有意义（放大时可能算出负数），一律夹到 0。
  return {
    top: Math.max(0, vv.offsetTop),
    bottom: Math.max(0, window.innerHeight - (vv.offsetTop + vv.height)),
    height: vv.height,
  }
}
