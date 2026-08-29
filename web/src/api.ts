// 与 Go 侧的结构一一对应。改后端结构时这里要跟着改。

export interface Project {
  id: number
  slug: string
  name: string
  rootPath?: string
  color?: string
  createdAt: number
  docCount: number
  unreadCount: number
}

export interface Doc {
  id: number
  projectId: number
  projectName?: string
  projectSlug?: string
  sourceKey: string
  sourcePath?: string
  title: string
  summary?: string
  kind: 'markdown' | 'html'
  renderMode?: 'reader' | 'raw'
  seq: number
  chars: number
  read: boolean
  readRatio: number
  later: boolean
  tags: string[]
  createdAt: number
  updatedAt: number
}

export interface Heading {
  level: number
  text: string
  blk: string
}

export interface Version {
  id: number
  seq: number
  chars: number
  bytes: number
  createdAt: number
}

export interface DocDetail extends Doc {
  html: string
  toc: string
  versions: Version[]
  annotations: Annotation[]
}

export interface Tag {
  id: number
  name: string
  color?: string
  count: number
}

export interface WatchStatus {
  roots?: string[]
  dirs: number
  failed: number
  degraded: boolean
  message?: string
}

export interface Status {
  total: number
  unread: number
  watch?: WatchStatus
  /** 服务端内嵌的前端主脚本文件名，用来识别浏览器是不是在跑缓存里的旧版。 */
  build?: string
}

export type FeedbackStatus = 'open' | 'fixed' | 'wontfix'

export const FEEDBACK_LABEL: Record<FeedbackStatus, string> = {
  open: '待修复',
  fixed: '已修复',
  wontfix: '无需修复',
}

export interface Feedback {
  id: number
  body: string
  status: FeedbackStatus
  resolution?: string
  docId?: number
  docTitle?: string
  route?: string
  /** 提交时的环境快照，JSON 字符串。服务端原样透传。 */
  env?: string
  createdAt: number
  updatedAt: number
}

export interface SearchHit extends Doc {
  snippet: string
}

export interface ParsedQuery {
  Raw: string
  Terms?: string[]
  Tags?: string[]
  NotTags?: string[]
  Project?: string
  Kind?: string
  Unread?: boolean
  Read?: boolean
  Later?: boolean
}

/** 时间线里的一组文档：有 agent 会话时按会话分，否则按「日期 + 项目」降级。 */
export interface TimelineGroup {
  key: string
  runId?: number
  runLabel?: string
  projectId: number
  projectName: string
  at: number
  unread: number
  docs: Doc[]
}

export type AnnotationKind = 'highlight' | 'note' | 'todo' | 'question'
export type AnnotationState = 'ok' | 'moved' | 'orphan'

export interface Annotation {
  id: number
  docId: number
  docTitle?: string
  projectName?: string
  kind: AnnotationKind
  color?: string
  body?: string
  blk: string
  startOff: number
  endOff: number
  quote: string
  state: AnnotationState
  orphanNote?: string
  createdAt: number
  updatedAt: number
}

/** 两个版本之间的块级差异。changed 是新版本里需要重读的块。 */
export interface VersionDiff {
  fromSeq: number
  toSeq: number
  changed: string[]
  removed: number
  same: number
}

export class AuthError extends Error {}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'same-origin', ...init })
  if (res.status === 401) throw new AuthError('未登录')
  if (!res.ok) {
    let message = `请求失败 (${res.status})`
    try {
      const body = await res.json()
      if (body?.error) message = body.error
    } catch {
      /* 响应体不是 JSON，用默认文案 */
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export interface DocQuery {
  project?: number
  tag?: string
  unread?: boolean
  later?: boolean
}

function docQueryString(q: DocQuery): string {
  const params = new URLSearchParams()
  if (q.project) params.set('project', String(q.project))
  if (q.tag) params.set('tag', q.tag)
  if (q.unread) params.set('unread', '1')
  if (q.later) params.set('later', '1')
  const s = params.toString()
  return s ? `?${s}` : ''
}

export const api = {
  login: (token: string) =>
    request<{ ok: boolean }>('/api/v1/session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token }),
    }),

  // 用一次性配对码换一个属于本设备的长期会话。
  // 和 login 的区别在于服务端那边：login 用的是主口令，
  // 而这里换到的是这台设备自己的凭据，换主口令时不会被牵连。
  pair: (code: string) =>
    request<{ ok: boolean; device: { id: number; name: string } }>('/api/v1/pair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code }),
    }),

  status: () => request<Status>('/api/v1/status'),
  projects: () => request<Project[]>('/api/v1/projects'),
  tags: () => request<Tag[]>('/api/v1/tags'),
  docs: (q: DocQuery = {}) => request<Doc[]>(`/api/v1/docs${docQueryString(q)}`),
  doc: (id: number) => request<DocDetail>(`/api/v1/docs/${id}`),

  patchDoc: (id: number, patch: { readRatio?: number; read?: boolean; later?: boolean }) =>
    request<Doc>(`/api/v1/docs/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    }),

  // forget=true 表示不留删除标记：源文件以后再出现时还想收。
  // 默认留标记，否则源文件还在被监听目录里的话，下次启动扫描原样收回，
  // 删除就成了假动作。
  deleteDoc: (id: number, forget = false) =>
    request<{ ok: boolean; tombstone: boolean }>(
      `/api/v1/docs/${id}${forget ? '?forget=1' : ''}`,
      { method: 'DELETE' },
    ),

  feedback: (status?: FeedbackStatus | 'all') =>
    request<Feedback[]>(
      `/api/v1/feedback${status && status !== 'all' ? `?status=${status}` : ''}`,
    ),

  createFeedback: (f: {
    body: string
    docId?: number
    docTitle?: string
    route?: string
    env?: unknown
  }) =>
    request<Feedback>('/api/v1/feedback', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(f),
    }),

  patchFeedback: (id: number, patch: { status: FeedbackStatus; resolution?: string }) =>
    request<Feedback>(`/api/v1/feedback/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    }),

  deleteFeedback: (id: number) =>
    request<{ ok: boolean }>(`/api/v1/feedback/${id}`, { method: 'DELETE' }),

  timeline: () => request<TimelineGroup[]>('/api/v1/timeline'),

  search: (q: string) =>
    request<{ query: ParsedQuery; hits: SearchHit[] }>(
      `/api/v1/search?q=${encodeURIComponent(q)}`,
    ),

  setTags: (id: number, tags: string[]) =>
    request<{ tags: string[] }>(`/api/v1/docs/${id}/tags`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tags }),
    }),

  createAnnotation: (
    docId: number,
    a: { kind: AnnotationKind; body?: string; blk: string; startOff: number; endOff: number; exact: string },
  ) =>
    request<Annotation>(`/api/v1/docs/${docId}/annotations`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(a),
    }),

  patchAnnotation: (id: number, patch: { kind?: AnnotationKind; body?: string }) =>
    request<Annotation>(`/api/v1/annotations/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    }),

  deleteAnnotation: (id: number) =>
    request<{ ok: boolean }>(`/api/v1/annotations/${id}`, { method: 'DELETE' }),

  rebindAnnotation: (
    id: number,
    a: { blk: string; startOff: number; endOff: number; exact: string },
  ) =>
    request<Annotation>(`/api/v1/annotations/${id}/rebind`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(a),
    }),

  actionable: () => request<Annotation[]>('/api/v1/annotations'),

  diff: (docId: number, from: number, to: number) =>
    request<VersionDiff>(`/api/v1/docs/${docId}/diff?from=${from}&to=${to}`),

  rawURL: (versionId: number) => `/api/v1/raw/${versionId}`,

  /** 原始文件下载。带图片的文档会打包成 zip，服务端决定。 */
  downloadURL: (docId: number) => `/api/v1/docs/${docId}/download`,

  /**
   * 把前端生成的导出物交给服务端，换一个下载地址。
   *
   * 为什么不直接下载 Blob：iOS Safari 对 <a download> 的支持时好时坏，
   * 而「真实 URL + Content-Disposition」一直可靠。
   */
  stageExport: (filename: string, mimeType: string, content: string, format?: 'pdf') =>
    request<{ url: string; expiresIn: number }>('/api/v1/export', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ filename, mime: mimeType, content, format }),
    }),
}

export const KIND_LABEL: Record<AnnotationKind, string> = {
  highlight: '高亮',
  note: '笔记',
  todo: '待办',
  question: '疑问',
}

/** 解析服务端存成 JSON 字符串的目录。 */
export function parseTOC(toc: string): Heading[] {
  try {
    const parsed = JSON.parse(toc)
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}
