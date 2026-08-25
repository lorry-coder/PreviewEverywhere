import { useCallback, useEffect, useRef, useState } from 'react'
import {
  api,
  AuthError,
  parseTOC,
  type Annotation,
  type AnnotationKind,
  type DocDetail,
  type Project,
  type Status,
  type Tag,
} from './api'
import type { Selected } from './annotate'
import AnnotationPanel from './components/AnnotationPanel'
import StaleBanner from './components/StaleBanner'
import Login from './components/Login'
import SearchBox from './components/SearchBox'
import Sidebar from './components/Sidebar'
import Actionable from './views/Actionable'
import Diag from './views/Diag'
import Feedback from './views/Feedback'
import DocList from './views/DocList'
import Reader, { TOCItem } from './views/Reader'
import SearchResults from './views/SearchResults'
import Timeline from './views/Timeline'
import { navigate, useRoute } from './router'

type Auth = 'checking' | 'in' | 'out'

export default function App() {
  const route = useRoute()
  const [auth, setAuth] = useState<Auth>('checking')
  const [status, setStatus] = useState<Status | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [tags, setTags] = useState<Tag[]>([])
  const [menuOpen, setMenuOpen] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  const [tocDoc, setTocDoc] = useState<DocDetail | null>(null)
  const mainRef = useRef<HTMLDivElement>(null)

  // 批注状态放在 App：右栏面板和正文里的高亮层分属不同的 grid 列，
  // 得有一个共同的持有者。
  const [annotations, setAnnotations] = useState<Annotation[]>([])
  const [activeAnnotation, setActiveAnnotation] = useState<number | null>(null)
  const [rebinding, setRebinding] = useState<Annotation | null>(null)

  const refresh = useCallback(() => setReloadKey((k) => k + 1), [])

  const handleDoc = useCallback((d: DocDetail) => {
    setTocDoc(d)
    setAnnotations(d.annotations || [])
    setActiveAnnotation(null)
    setRebinding(null)
  }, [])

  // 扫码落地时地址是 http://host/#t=<token>：换成 Cookie 后立刻把它从
  // 地址栏抹掉，免得口令留在历史记录里。
  useEffect(() => {
    const match = location.hash.match(/^#t=([0-9a-fA-F]+)$/)
    const boot = async () => {
      if (match) {
        try {
          await api.login(match[1])
        } catch {
          /* 口令无效就走正常登录流程 */
        }
        history.replaceState(null, '', location.pathname + location.search + '#/')
      }
      try {
        setStatus(await api.status())
        setAuth('in')
      } catch (err) {
        setAuth(err instanceof AuthError ? 'out' : 'in')
      }
    }
    boot()
  }, [])

  useEffect(() => {
    if (auth !== 'in') return
    Promise.all([api.status(), api.projects(), api.tags()])
      .then(([s, p, t]) => {
        setStatus(s)
        setProjects(p)
        setTags(t)
      })
      .catch((err) => {
        if (err instanceof AuthError) setAuth('out')
      })
  }, [auth, reloadKey])

  // 文档一入库就推过来，页面开着的时候不用下拉刷新。
  useEffect(() => {
    if (auth !== 'in') return
    const es = new EventSource('/api/v1/events')
    es.addEventListener('doc', () => refresh())
    return () => es.close()
  }, [auth, refresh])

  // 切换文档时回到顶部。刻意只依赖 docId——早先这里还依赖 reloadKey，
  // 于是读到一半被自动标记已读（reloadKey++）就会被弹回顶部。
  const docId = route.name === 'doc' ? route.id : 0
  useEffect(() => {
    mainRef.current?.scrollTo({ top: 0 })
    if (!docId) {
      setTocDoc(null)
      setAnnotations([])
      setRebinding(null)
    }
  }, [docId])

  // ── 批注操作 ────────────────────────────────────────────────
  const createAnnotation = useCallback(
    async (id: number, sel: Selected, kind: AnnotationKind, body: string) => {
      const created = await api.createAnnotation(id, {
        kind,
        body,
        blk: sel.blk,
        startOff: sel.startOff,
        endOff: sel.endOff,
        exact: sel.exact,
      })
      setAnnotations((prev) => [...prev, created])
      // 刻意不选中它。以前选中只是给高亮加个强调样式，无伤大雅；
      // 现在选中会弹出批注卡片，于是「划词 → 点高亮」之后会立刻冒出一张
      // 写着「这条只有高亮，没有写内容」的卡片挡在正文上。
      // 高亮本身出现就是足够的确认了。
    },
    [],
  )

  const deleteAnnotation = useCallback(async (a: Annotation) => {
    await api.deleteAnnotation(a.id)
    setAnnotations((prev) => prev.filter((x) => x.id !== a.id))
  }, [])

  // 改一条批注的正文。手机上是「点开高亮 → 编辑」这条路的落点；
  // 在这之前批注写完就只能读不能改。
  const updateAnnotationBody = useCallback(async (a: Annotation, body: string) => {
    const updated = await api.patchAnnotation(a.id, { body })
    setAnnotations((prev) => prev.map((x) => (x.id === updated.id ? updated : x)))
  }, [])

  const rebindAnnotation = useCallback(
    async (sel: Selected) => {
      if (!rebinding) return
      const updated = await api.rebindAnnotation(rebinding.id, {
        blk: sel.blk,
        startOff: sel.startOff,
        endOff: sel.endOff,
        exact: sel.exact,
      })
      setAnnotations((prev) => prev.map((x) => (x.id === updated.id ? updated : x)))
      setRebinding(null)
      setActiveAnnotation(updated.id)
    },
    [rebinding],
  )

  const scrollToAnnotation = useCallback((a: Annotation) => {
    setActiveAnnotation(a.id)
    document
      .querySelector(`[data-blk="${CSS.escape(a.blk)}"]`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }, [])

  if (auth === 'checking') return <div className="loading">正在检查登录状态…</div>
  if (auth === 'out') return <Login onDone={() => setAuth('in')} />

  const toc = tocDoc ? parseTOC(tocDoc.toc) : []
  const isReadable = route.name === 'doc' && tocDoc?.renderMode !== 'raw'
  const showAside = isReadable && (toc.length > 0 || annotations.length > 0)

  return (
    <div className="app">
      <header className="topbar">
        <button
          className="icon-btn menu-btn"
          onClick={() => setMenuOpen(!menuOpen)}
          aria-label="打开导航"
        >
          ☰
        </button>
        <button className="logo" onClick={() => navigate('#/')}>
          PREVIEW·EVERYWHERE
        </button>
        <SearchBox route={route} />
        {status && status.unread > 0 && (
          <button className="topbar-stat" onClick={() => navigate('#/unread')}>
            <b>{status.unread}</b> 未读
          </button>
        )}
      </header>

      <StaleBanner serverBuild={status?.build} />

      <div className={`body${showAside ? ' with-toc' : ''}`}>
        <Sidebar
          route={route}
          projects={projects}
          tags={tags}
          status={status}
          open={menuOpen}
          onNavigate={() => setMenuOpen(false)}
        />
        <div
          className={`sidebar-backdrop${menuOpen ? ' open' : ''}`}
          onClick={() => setMenuOpen(false)}
        />

        <main className="main" ref={mainRef}>
          {route.name === 'doc' ? (
            <Reader
              docId={route.id}
              scrollRef={mainRef}
              allTags={tags}
              onChanged={refresh}
              onDoc={handleDoc}
              annotations={annotations}
              activeAnnotation={activeAnnotation}
              onPickAnnotation={setActiveAnnotation}
              onCreateAnnotation={createAnnotation}
              rebinding={rebinding}
              onRebind={rebindAnnotation}
              onCancelRebind={() => setRebinding(null)}
              onDeleteAnnotation={deleteAnnotation}
              onUpdateAnnotationBody={updateAnnotationBody}
              onStartRebind={(a) => {
                setRebinding(a)
                setActiveAnnotation(a.id)
              }}
            />
          ) : route.name === 'timeline' ? (
            <Timeline status={status} reloadKey={reloadKey} />
          ) : route.name === 'actionable' ? (
            <Actionable reloadKey={reloadKey} />
          ) : route.name === 'diag' ? (
            <Diag status={status} />
          ) : route.name === 'feedback' ? (
            <Feedback />
          ) : route.name === 'search' ? (
            <SearchResults q={route.q} tags={tags} projects={projects} />
          ) : (
            <DocList route={route} status={status} reloadKey={reloadKey} />
          )}
        </main>

        {showAside && (
          <aside className="toc">
            {toc.length > 0 && (
              <>
                <div className="toc-group">大纲</div>
                {toc.map((h) => (
                  <TOCItem key={h.blk} heading={h} />
                ))}
                <div className="aside-hr" />
              </>
            )}
            <AnnotationPanel
              annotations={annotations}
              activeId={activeAnnotation}
              onPick={scrollToAnnotation}
              onDelete={deleteAnnotation}
              onStartRebind={(a) => {
                setRebinding(a)
                setActiveAnnotation(a.id)
              }}
            />
          </aside>
        )}
      </div>
    </div>
  )
}
