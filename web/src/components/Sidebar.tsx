import type { Project, Status, Tag } from '../api'
import { navigate, routeKey, type Route } from '../router'

interface Props {
  route: Route
  projects: Project[]
  tags: Tag[]
  status: Status | null
  open: boolean
  onNavigate: () => void
}

/**
 * 侧栏是「空间轴」：项目与标签，长期稳定。
 * 与之正交的「时间轴」（按 agent 会话分组的首页时间线）在 P2。
 */
export default function Sidebar({ route, projects, tags, status, open, onNavigate }: Props) {
  const active = routeKey(route)

  const go = (hash: string) => {
    navigate(hash)
    onNavigate()
  }

  return (
    <nav className={`sidebar${open ? ' open' : ''}`}>
      <div className="side-group">文档</div>

      <button
        className={`side-item${active === 'timeline' ? ' active' : ''}`}
        onClick={() => go('#/')}
      >
        <span className="label">时间线</span>
      </button>

      <button className={`side-item${active === 'all' ? ' active' : ''}`} onClick={() => go('#/all')}>
        <span className="label">全部文档</span>
        {status && <span className="count">{status.total}</span>}
      </button>

      <button
        className={`side-item${active === 'unread' ? ' active' : ''}`}
        onClick={() => go('#/unread')}
      >
        <span className="label">未读</span>
        {status && status.unread > 0 && <span className="badge">{status.unread}</span>}
      </button>

      <button
        className={`side-item${active === 'later' ? ' active' : ''}`}
        onClick={() => go('#/later')}
      >
        <span className="label">稍后读</span>
      </button>

      <button
        className={`side-item${active === 'actionable' ? ' active' : ''}`}
        onClick={() => go('#/todo')}
      >
        <span className="label">待办与疑问</span>
      </button>

      {projects.length > 0 && (
        <>
          <div className="side-group">项目</div>
          {projects.map((p) => (
            <button
              key={p.id}
              className={`side-item${active === `project:${p.id}` ? ' active' : ''}`}
              onClick={() => go(`#/project/${p.id}`)}
              title={p.rootPath || p.name}
            >
              <span className="dot" style={p.color ? { background: p.color } : undefined} />
              <span className="label">{p.name}</span>
              {p.unreadCount > 0 ? (
                <span className="badge">{p.unreadCount}</span>
              ) : (
                <span className="count">{p.docCount}</span>
              )}
            </button>
          ))}
        </>
      )}

      {tags.length > 0 && (
        <>
          <div className="side-group">标签</div>
          {tags.map((t) => (
            <button
              key={t.id}
              className={`side-item${active === `tag:${t.name}` ? ' active' : ''}`}
              onClick={() => go(`#/tag/${encodeURIComponent(t.name)}`)}
            >
              <span className="label">{t.name}</span>
              <span className="count">{t.count}</span>
            </button>
          ))}
        </>
      )}
      {/* 放在最下面：平时用不到，出问题时得找得到。
          这个平台跑在手机上，而手机上的问题在开发机上复现不了，
          没有这一页就只能靠来回描述现象。 */}
      <div className="side-group">诊断</div>
      <button
        className={`side-item${active === 'feedback' ? ' active' : ''}`}
        onClick={() => go('#/feedback')}
      >
        <span className="label">问题反馈</span>
      </button>
      <button
        className={`side-item${active === 'diag' ? ' active' : ''}`}
        onClick={() => go('#/diag')}
      >
        <span className="label">环境自查</span>
      </button>
    </nav>
  )
}
