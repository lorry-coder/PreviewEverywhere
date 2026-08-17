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
