import { useEffect, useState } from 'react'

// 自写的极简 hash 路由。用 hash 而不是 history 有个具体理由：
// 扫码登录落地时地址是 http://host/#t=<token>，hash 本来就要被读一次。
// 全套路由库在这里只有四条路由，不值当。

export type Route =
  | { name: 'timeline' }
  | { name: 'actionable' }
  | { name: 'search'; q: string }
  | { name: 'list'; project?: number; tag?: string; unread?: boolean; later?: boolean }
  | { name: 'doc'; id: number }

export function parseHash(hash: string = location.hash): Route {
  const path = hash.replace(/^#\/?/, '')
  const [head, ...rest] = path.split('/')
  const tail = rest.join('/')

  switch (head) {
    case 'doc': {
      const id = Number(tail)
      if (Number.isFinite(id) && id > 0) return { name: 'doc', id }
      return { name: 'list' }
    }
    case 'project': {
      const id = Number(tail)
      return Number.isFinite(id) && id > 0 ? { name: 'list', project: id } : { name: 'list' }
    }
    case 'tag':
      return tail ? { name: 'list', tag: decodeURIComponent(tail) } : { name: 'list' }
    case 'unread':
      return { name: 'list', unread: true }
    case 'later':
      return { name: 'list', later: true }
    case 'all':
      return { name: 'list' }
    case 'todo':
      return { name: 'actionable' }
    case 'search':
      return { name: 'search', q: decodeURIComponent(tail) }
    default:
      // 首页是时间线，不是文档列表：面对持续流入的运行记录，
      // 「昨晚那次跑出了什么」比「这个项目下的全部文档」更常用。
      return { name: 'timeline' }
  }
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseHash())
  useEffect(() => {
    const onChange = () => setRoute(parseHash())
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return route
}

export function navigate(to: string, replace = false) {
  if (location.hash === to) return
  if (replace) {
    // 边打字边搜时不要往历史里塞一堆中间状态。
    history.replaceState(null, '', location.pathname + location.search + to)
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    return
  }
  location.hash = to
}

/** 判断侧栏某一项是否处于选中态。 */
export function routeKey(route: Route): string {
  if (route.name === 'timeline') return 'timeline'
  if (route.name === 'actionable') return 'actionable'
  if (route.name === 'search') return 'search'
  if (route.name === 'doc') return 'doc'
  if (route.project) return `project:${route.project}`
  if (route.tag) return `tag:${route.tag}`
  if (route.unread) return 'unread'
  if (route.later) return 'later'
  return 'all'
}
