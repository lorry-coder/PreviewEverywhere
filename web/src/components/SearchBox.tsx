import { useEffect, useRef, useState } from 'react'
import { navigate, type Route } from '../router'

/**
 * 顶栏搜索框。边打字边搜，但用 replaceState 跳转——
 * 否则搜一次会往历史里塞十几条中间状态，回退键就废了。
 */
export default function SearchBox({ route }: { route: Route }) {
  const [value, setValue] = useState(route.name === 'search' ? route.q : '')
  const inputRef = useRef<HTMLInputElement>(null)
  const typing = useRef(false)

  // 从别处跳进搜索页（比如点了标签）时，把输入框同步过来。
  useEffect(() => {
    if (route.name === 'search' && !typing.current) setValue(route.q)
    if (route.name !== 'search' && !typing.current) setValue('')
  }, [route])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
      }
      if (e.key === 'Escape' && document.activeElement === inputRef.current) {
        inputRef.current?.blur()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    if (!typing.current) return
    const timer = window.setTimeout(() => {
      typing.current = false
      if (value.trim()) {
        navigate(`#/search/${encodeURIComponent(value.trim())}`, true)
      } else if (route.name === 'search') {
        navigate('#/search/', true)
      }
    }, 250)
    return () => window.clearTimeout(timer)
  }, [value, route.name])

  return (
    <div className="searchbox">
      <span className="searchbox-icon" aria-hidden="true">
        ⌕
      </span>
      <input
        ref={inputRef}
        type="search"
        value={value}
        placeholder="搜索文档、标签…"
        aria-label="搜索"
        spellCheck={false}
        onChange={(e) => {
          typing.current = true
          setValue(e.target.value)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && value.trim()) {
            typing.current = false
            navigate(`#/search/${encodeURIComponent(value.trim())}`)
          }
        }}
      />
      <kbd className="searchbox-kbd">⌘K</kbd>
    </div>
  )
}
