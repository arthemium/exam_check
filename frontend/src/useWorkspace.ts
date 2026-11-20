import { useCallback, useEffect, useRef, useState } from 'react';
import type { Repository, Session, Workspace } from './types';

const emptyWorkspace = (): Workspace => ({ folders: [], exams: [] });
export const errorMessage = (error: unknown) => error instanceof Error ? error.message : 'The request could not be completed.';

export function useWorkspace(repository: Repository) {
  const [workspace, setWorkspace] = useState<Workspace>(emptyWorkspace);
  const [session, setSession] = useState<Session | null>(null);
  const [initializing, setInitializing] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [refreshError, setRefreshError] = useState('');
  const inFlight = useRef<Promise<void> | null>(null);
  const generation = useRef(0);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    if (inFlight.current) await inFlight.current.catch(() => undefined);
    const current = generation.current;
    const request = repository.load().then(value => {
      if (mounted.current && current === generation.current) { setWorkspace(value); setRefreshError(''); }
    });
    inFlight.current = request;
    try { await request; } finally { if (inFlight.current === request) inFlight.current = null; }
  }, [repository]);

  const handleError = useCallback((failure: unknown) => {
    if (!mounted.current) return;
    setError(errorMessage(failure));
    if ((failure as { status?: number })?.status === 401) {
      generation.current += 1;
      setSession(null);
      setWorkspace(emptyWorkspace());
    }
  }, []);

  const handleRefreshError = useCallback((failure: unknown) => {
    if (!mounted.current) return;
    setRefreshError(`The view could not be updated. ${errorMessage(failure)}`);
    if ((failure as { status?: number })?.status === 401) handleError(failure);
  }, [handleError]);

  useEffect(() => {
    mounted.current = true;
    let cancelled = false;
    repository.session().then(async user => {
      if (cancelled) return;
      setSession(user);
      if (user) await refresh().catch(handleRefreshError);
    }).catch(handleError).finally(() => { if (!cancelled) setInitializing(false); });
    return () => { cancelled = true; mounted.current = false; generation.current += 1; };
  }, [repository, refresh, handleError, handleRefreshError]);

  useEffect(() => {
    if (!session) return;
    const timer = window.setInterval(() => {
      if (!inFlight.current) void refresh().catch(handleRefreshError);
    }, 1200);
    return () => window.clearInterval(timer);
  }, [session, refresh, handleRefreshError]);

  const run = useCallback(async (operation: () => Promise<unknown>) => {
    setBusy(true);
    setError('');
    try {
      await operation();
      await refresh().catch(handleRefreshError);
      return true;
    }
    catch (failure) { handleError(failure); return false; }
    finally { if (mounted.current) setBusy(false); }
  }, [refresh, handleError, handleRefreshError]);

  async function login(username: string, password: string) {
    setBusy(true);
    setError('');
    try {
      const user = await repository.login(username, password);
      generation.current += 1;
      setSession(user);
      await refresh().catch(handleRefreshError);
    } catch (failure) { handleError(failure); }
    finally { setBusy(false); }
  }

  async function logout() {
    setBusy(true);
    try {
      await repository.logout();
      generation.current += 1;
      setSession(null);
      setWorkspace(emptyWorkspace());
      setError('');
      setRefreshError('');
    } catch (failure) { handleError(failure); }
    finally { setBusy(false); }
  }

  function dismissError(value: string) { setError(value); setRefreshError(''); }
  return { workspace, session, initializing, busy, error: error || refreshError, setError: dismissError, refresh, run, login, logout };
}
