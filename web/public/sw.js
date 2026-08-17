/**
 * PreviewEverywhere 的 service worker。
 *
 * 目标很具体：地铁上没信号时，之前打开过的文档还能读完，图片还在，
 * 划过的重点还看得见。所以缓存策略是按「这份东西会不会变」来分的：
 *
 *   /assets/*          构建产物，文件名带内容哈希 —— 缓存优先，永不过期
 *   /api/v1/asset/*    内容寻址的图片与附件 —— 同上
 *   /api/v1/docs/*     文档内容会随 agent 重跑而变 —— 网络优先，断网回落缓存
 *   index.html         要能拿到新版前端 —— 网络优先，断网回落缓存
 *   其余接口            搜索、推送、SSE —— 只走网络，缓存它们没有意义
 *
 * 刻意不做预缓存：产物文件名带哈希，静态的 sw.js 猜不到它们。
 * 反正你总得先联网打开一次，那一次之后运行时缓存就齐了。
 */

const SHELL = 'pe-shell-v1'
const RUNTIME = 'pe-runtime-v1'
const IMMUTABLE = 'pe-immutable-v1'
const RUNTIME_MAX = 300

self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting())
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keep = new Set([SHELL, RUNTIME, IMMUTABLE])
      const names = await caches.keys()
      await Promise.all(names.filter((n) => !keep.has(n)).map((n) => caches.delete(n)))
      await self.clients.claim()
    })(),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return

  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return

  // SSE 是长连接，推送与检索的结果也没有缓存价值。
  if (
    url.pathname === '/api/v1/events' ||
    url.pathname === '/api/v1/ingest' ||
    url.pathname === '/api/v1/search'
  ) {
    return
  }

  if (url.pathname.startsWith('/assets/') || url.pathname.startsWith('/api/v1/asset/')) {
    event.respondWith(cacheFirst(req, IMMUTABLE))
    return
  }

  if (url.pathname.startsWith('/api/')) {
    event.respondWith(networkFirst(req, RUNTIME))
    return
  }

  if (req.mode === 'navigate' || url.pathname === '/') {
    event.respondWith(networkFirst(req, SHELL))
    return
  }

  event.respondWith(cacheFirst(req, SHELL))
})

/** 内容不会变的东西：命中缓存就直接用，顺带补齐没缓存过的。 */
async function cacheFirst(req, cacheName) {
  const cache = await caches.open(cacheName)
  const hit = await cache.match(req)
  if (hit) return hit
  try {
    const res = await fetch(req)
    if (res.ok) cache.put(req, res.clone())
    return res
  } catch (err) {
    return offlineResponse(req)
  }
}

/** 会变的东西：优先拿新的，拿不到再用上次存的。 */
async function networkFirst(req, cacheName) {
  const cache = await caches.open(cacheName)
  try {
    const res = await fetch(req)
    if (res.ok) {
      cache.put(req, res.clone())
      trim(cache)
    }
    return res
  } catch (err) {
    const hit = await cache.match(req)
    if (hit) return hit
    return offlineResponse(req)
  }
}

/** 缓存条目上限，免得读了几千篇文档之后把手机存储吃光。 */
async function trim(cache) {
  const keys = await cache.keys()
  if (keys.length <= RUNTIME_MAX) return
  for (const key of keys.slice(0, keys.length - RUNTIME_MAX)) {
    await cache.delete(key)
  }
}

/**
 * 断网且没有缓存时给一个明确的回应，而不是让请求以网络错误告终——
 * 前端能把它当成正常的失败来展示。
 */
function offlineResponse(req) {
  const accepts = req.headers.get('accept') || ''
  if (accepts.includes('application/json') || new URL(req.url).pathname.startsWith('/api/')) {
    return new Response(JSON.stringify({ error: '离线：这份内容还没有缓存过' }), {
      status: 503,
      headers: { 'Content-Type': 'application/json; charset=utf-8' },
    })
  }
  return new Response('离线', { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
}
