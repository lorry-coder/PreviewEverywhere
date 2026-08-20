import { useEffect, useState } from 'react'

// 是不是触屏。唯一的用途是决定要不要给 iOS 的系统选区菜单让出位置——
// 桌面端没有那个东西，气泡贴着选区上方就行。
//
// 只认 pointer: coarse。刻意不认 hover: none：headless / 虚拟环境里
// 没有输入设备时它就是真，会让桌面端白白付出让位的代价。
const TOUCH_QUERY = '(pointer: coarse)'

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
