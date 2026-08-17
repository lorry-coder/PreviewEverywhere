import { useState } from 'react'
import { api } from '../api'

export default function Login({ onDone }: { onDone: () => void }) {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(token.trim())
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <h1>PreviewEverywhere</h1>
        <p>
          用手机扫 <code>pe serve</code> 或 <code>pe token</code> 打印的二维码即可登录，
          扫一次管一年。没有二维码时，把终端里那串口令粘到下面。
        </p>
        <input
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          placeholder="访问口令"
          autoFocus
          autoComplete="current-password"
          spellCheck={false}
        />
        <button type="submit" disabled={busy || !token.trim()}>
          {busy ? '正在登录…' : '登录'}
        </button>
        {error && <div className="error">{error}</div>}
      </form>
    </div>
  )
}
