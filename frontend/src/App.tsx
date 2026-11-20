import { HashRouter, Link, Route, Routes } from 'react-router-dom';
import type { Repository } from './types';
import { useWorkspace } from './useWorkspace';
import { AppContext } from './appContext';
import { LoginScreen } from './components/LoginScreen';
import { AppNavigation } from './components/AppNavigation';
import { FoldersPage } from './pages/FoldersPage';
import { FolderPage } from './pages/FolderPage';
import { CheckPage, ReviewPage } from './pages/ExamPages';

export function App({ repository }: { repository: Repository }) {
  const data = useWorkspace(repository);
  if (data.initializing) return <main className="loading" role="status">Opening examcheck…</main>;
  if (!data.session) return <LoginScreen onLogin={data.login} busy={data.busy} error={data.error} />;
  return <AppContext.Provider value={{ data, repository }}><HashRouter><div className="app">
    <AppNavigation />
    <main id="main-content" className="main-content">
      {data.error && <div className="notice error-notice" role="alert"><p>{data.error}</p><button className="button quiet" disabled={data.busy} onClick={() => void data.run(async () => {})}>Refresh</button><button className="button quiet" onClick={() => data.setError('')}>Dismiss</button></div>}
      <Routes><Route path="/" element={<FoldersPage />} /><Route path="/folders/:folderId" element={<FolderPage />} /><Route path="/folders/:folderId/check" element={<CheckPage />} /><Route path="/folders/:folderId/exams/:examId/check" element={<CheckPage />} /><Route path="/folders/:folderId/exams/:examId/review" element={<ReviewPage />} /><Route path="*" element={<div className="panel empty-state"><h1>Page not found</h1><Link to="/">Back to all folders</Link></div>} /></Routes>
    </main>
    <footer className="app-footer">{repository.capabilities.demo ? 'Demo workspace · sample checks are simulated' : `Signed in as ${data.session.displayName || data.session.username}`}</footer>
  </div></HashRouter></AppContext.Provider>;
}
