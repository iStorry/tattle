import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { api, onUnauthorized, type AuthState } from './api'
import App from './App'
import { Logo } from './parts'

/** Decides between first-time setup, the sign-in page and the dashboard. */
export function Root() {
  const [state, setState] = useState<AuthState | null>(null)
  const [failed, setFailed] = useState(false)

  const refresh = useCallback(() => {
    api
      .auth()
      .then((s) => {
        setState(s)
        setFailed(false)
      })
      .catch(() => setFailed(true))
  }, [])

  useEffect(() => {
    refresh()
    // Any request that is turned away means the session ended.
    onUnauthorized(refresh)
    return () => onUnauthorized(null)
  }, [refresh])

  if (!state) {
    return (
      <div className="splash">
        <Logo />
        <p>{failed ? 'Tattle is not answering. Is it still running on the host?' : 'Loading…'}</p>
      </div>
    )
  }
  if (state.setup) return <SetupPage onDone={refresh} />
  if (!state.signedIn) return <LoginPage onDone={refresh} />
  return (
    <App
      canSignOut={state.enabled}
      onSignOut={() => {
        api.logout().finally(refresh)
      }}
    />
  )
}

function AuthCard(p: { title: string; intro: ReactNode; onSubmit: () => Promise<void>; action: string; children: ReactNode }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await p.onSubmit()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Something went wrong.')
      setBusy(false)
    }
  }

  return (
    <div className="auth">
      <form className="card auth-card" onSubmit={submit}>
        <Logo />
        <h1>{p.title}</h1>
        <div className="help auth-intro">{p.intro}</div>
        {/* Lets password managers file the password under a name. */}
        <input type="text" name="username" autoComplete="username" value="tattle" readOnly hidden />
        {p.children}
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button className="button primary" disabled={busy}>
          {busy ? 'One moment…' : p.action}
        </button>
      </form>
    </div>
  )
}

function Field(p: { label: string; value: string; onChange: (v: string) => void; type?: string; autoComplete: string; autoFocus?: boolean; hint?: string }) {
  return (
    <label className="field">
      <span className="field-label">{p.label}</span>
      <input
        className="input"
        type={p.type ?? 'password'}
        value={p.value}
        onChange={(e) => p.onChange(e.target.value)}
        autoComplete={p.autoComplete}
        autoFocus={p.autoFocus}
        autoCapitalize="off"
        spellCheck={false}
        required
      />
      {p.hint && <span className="help">{p.hint}</span>}
    </label>
  )
}

function SetupPage({ onDone }: { onDone: () => void }) {
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [repeat, setRepeat] = useState('')

  return (
    <AuthCard
      title="Set up Tattle"
      action="Set password"
      intro={
        <>
          <p>Choose a password for this dashboard. To show that the host is yours, enter the setup code from its log:</p>
          <pre>journalctl -u tattle | grep "setup code" | tail -1</pre>
        </>
      }
      onSubmit={async () => {
        if (password !== repeat) throw new Error('The two passwords are not the same.')
        await api.setup(code, password)
        onDone()
      }}
    >
      <Field label="Setup code" type="text" value={code} onChange={setCode} autoComplete="off" autoFocus />
      <Field label="New password" value={password} onChange={setPassword} autoComplete="new-password" hint="At least 8 characters." />
      <Field label="Repeat the password" value={repeat} onChange={setRepeat} autoComplete="new-password" />
    </AuthCard>
  )
}

function LoginPage({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState('')
  return (
    <AuthCard
      title="Sign in"
      action="Sign in"
      intro={<p>Enter the dashboard password.</p>}
      onSubmit={async () => {
        await api.login(password)
        onDone()
      }}
    >
      <Field label="Password" value={password} onChange={setPassword} autoComplete="current-password" autoFocus />
      <p className="help">
        Forgotten it? Run <code>tattle reset-password</code> on the host, then set a new one here.
      </p>
    </AuthCard>
  )
}

/** Lets a signed-in owner change the password, from the settings page. */
export function PasswordCard() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [state, setState] = useState<'idle' | 'saving' | 'saved'>('idle')
  const [error, setError] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    if (next !== repeat) {
      setError('The two new passwords are not the same.')
      return
    }
    setState('saving')
    try {
      await api.changePassword(current, next)
      setCurrent('')
      setNext('')
      setRepeat('')
      setState('saved')
    } catch (err) {
      setState('idle')
      setError(err instanceof Error ? err.message : 'The password could not be changed.')
    }
  }

  return (
    <form className="card" onSubmit={submit}>
      <h2>Password</h2>
      <p className="help">Changing the password signs out every other browser.</p>
      <input type="text" name="username" autoComplete="username" value="tattle" readOnly hidden />
      <Field label="Current password" value={current} onChange={setCurrent} autoComplete="current-password" />
      <Field label="New password" value={next} onChange={setNext} autoComplete="new-password" hint="At least 8 characters." />
      <Field label="Repeat the new password" value={repeat} onChange={setRepeat} autoComplete="new-password" />
      <div className="actions">
        <button className="button" disabled={state === 'saving'}>
          {state === 'saving' ? 'Changing…' : 'Change password'}
        </button>
        {state === 'saved' && <span role="status">Password changed.</span>}
        {error && (
          <span role="alert" className="error">
            {error}
          </span>
        )}
      </div>
    </form>
  )
}
