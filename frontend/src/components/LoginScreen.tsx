import { useState, type FormEvent } from 'react';

interface Props { onLogin: (username: string, password: string) => Promise<void>; busy: boolean; error: string }
export function LoginScreen({ onLogin, busy, error }: Props) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  async function submit(event: FormEvent) {
    event.preventDefault();
    await onLogin(username.trim(), password);
    setPassword('');
  }
  return <main className="login-page"><form className="panel login-form" onSubmit={submit}>
    <span className="eyebrow">EXAMCHECK</span><h1>Sign in</h1><p className="muted">Use your examcheck username and password.</p>
    <label className="field">Username<input autoComplete="username" required value={username} onChange={event => setUsername(event.target.value)} autoFocus /></label>
    <label className="field">Password<input type="password" autoComplete="current-password" required value={password} onChange={event => setPassword(event.target.value)} /></label>
    {error && <p className="form-error" role="alert">{error}</p>}
    <button className="button primary" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form></main>;
}
