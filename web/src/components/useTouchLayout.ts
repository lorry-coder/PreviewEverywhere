import { useEffect, useState } from 'react'
import { DOCK_MAX_WIDTH } from '../popupPlacement'

// 什么时候该走「贴边」这套布局。两个条件取或：
//
//   pointer: coarse —— 触屏。iOS 会在选区旁边画自己的编辑菜单来抢位置。
//   视口够窄        —— 浮动气泡在这个宽度下必然有一半溢出屏幕。
//
// 第二条不是第一条的补充说明，它自己就能成立：窄窗口的桌面浏览器同样会被
// 气泡溢出坑到。反过来它也是第一条的保险——万一哪个浏览器不报 coarse，
// 手机屏的宽度也会把它兜住。
//
// 刻意不认 hover: none：headless / 虚拟环境里没有输入设备时它就是真，
// 结果 1440 宽的窗口也贴边，白白牺牲桌面端的体验换一个假想中的收益。
const TOUCH_QUERY = `(pointer: coarse), (max-width: ${DOCK_MAX_WIDTH - 1}px)`

/** 订阅上述条件，窗口缩放或设备旋转时会跟着变。 */
export function useTouchLayout(): boolean {
  const [on, setOn] = useState(() => window.matchMedia?.(TOUCH_QUERY).matches ?? false)
  useEffect(() => {
    const mq = window.matchMedia?.(TOUCH_QUERY)
    if (!mq) return
    const onChange = () => setOn(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return on
}
