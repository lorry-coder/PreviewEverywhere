import { useEffect, useRef, useState } from 'react'
import { api, type Tag } from '../api'
import { navigate } from '../router'

interface Props {
  docId: number
  tags: string[]
  allTags: Tag[]
  onChange: (tags: string[]) => void
}

/**
 * 阅读页上的标签增删。
 *
 * 删掉一个来自 front-matter 的标签时，服务端会留一条墓碑，
 * 所以 agent 下次重新生成这篇文档不会把它又加回来——
 * 没有这个机制的话，手动整理标签就是白干。
 */
export default function TagEditor({ docId, tags, allTags, onChange }: Props) {
  const [adding, setAdding] = useState(false)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (adding) inputRef.current?.focus()
  }, [adding])

  async function commit(next: string[]) {
    setBusy(true)
    try {
      const res = await api.setTags(docId, next)
      onChange(res.tags)
    } catch {
      /* 失败就保持原样，下次进页面会重新拉到真实状态 */
    } finally {
      setBusy(false)
    }
  }

  const add = () => {
    const name = draft.trim()
    setDraft('')
    setAdding(false)
    if (name && !tags.includes(name)) commit([...tags, name])
  }

  return (
    <>
      {tags.map((t) => (
        <span key={t} className="tag-chip">
          <button
            className="tag-chip-name"
            onClick={() => navigate(`#/tag/${encodeURIComponent(t)}`)}
            title={`查看所有带「${t}」的文档`}
          >
            {t}
          </button>
          <button
            className="tag-chip-x"
            disabled={busy}
            aria-label={`移除标签 ${t}`}
            onClick={() => commit(tags.filter((x) => x !== t))}
          >
            ×
          </button>
        </span>
      ))}

      {adding ? (
        <input
          ref={inputRef}
          className="tag-input"
          list="pe-all-tags"
          value={draft}
          placeholder="标签名"
          spellCheck={false}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={add}
          onKeyDown={(e) => {
            if (e.key === 'Enter') add()
            if (e.key === 'Escape') {
              setDraft('')
              setAdding(false)
            }
          }}
        />
      ) : (
        <button className="tag-add" disabled={busy} onClick={() => setAdding(true)}>
          + 标签
        </button>
      )}

      <datalist id="pe-all-tags">
        {allTags.map((t) => (
          <option key={t.id} value={t.name} />
        ))}
      </datalist>
    </>
  )
}
