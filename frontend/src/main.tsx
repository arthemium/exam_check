import { Component, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createRepository } from './data';
import './styles.css';

class AppErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    return this.state.failed ? <main className="panel empty-state"><h1>Something went wrong</h1><p>Your saved exams remain in storage. Reload to try again.</p><button className="button" onClick={() => window.location.reload()}>Reload</button></main> : this.props.children;
  }
}

const repository = createRepository(import.meta.env.VITE_DATA_MODE === 'demo' ? 'demo' : 'api');
createRoot(document.getElementById('root')!).render(<AppErrorBoundary><App repository={repository} /></AppErrorBoundary>);
