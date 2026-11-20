import { Link, matchPath, useLocation } from 'react-router-dom';
import { useApp } from '../appContext';

export function AppNavigation() {
  const { data, repository } = useApp();
  const { pathname, search } = useLocation();
  const folderId = matchPath('/folders/:folderId/*', pathname)?.params.folderId;
  const examId = matchPath('/folders/:folderId/exams/:examId/*', pathname)?.params.examId;
  const folder = data.workspace.folders.find(item => item.id === folderId);
  const exam = data.workspace.exams.find(item => item.id === examId && item.folderId === folderId);
  const folderPath = folder ? `/folders/${folder.id}` : '/';
  const inExam = pathname.endsWith('/check') || pathname.endsWith('/review');
  const reviewing = pathname.endsWith('/review');
  return <>
    <header className="app-header"><Link className="brand" to="/" aria-label="Examcheck folders"><span className="brand-symbol">e</span>examcheck</Link><span className="tag">{repository.capabilities.demo ? 'Demo' : 'Workspace'}</span>
      <nav className="main-nav" aria-label="Main navigation"><Link className={`button ${pathname === '/' ? 'primary' : ''}`} to="/" aria-current={pathname === '/' ? 'page' : undefined}>All folders</Link>{folder && <Link className="button primary" to={folderPath} aria-current={pathname === folderPath ? 'page' : undefined}>Exam list</Link>}</nav>
      {!repository.capabilities.demo && <button className="button quiet" onClick={() => void data.logout()} disabled={data.busy}>Sign out</button>}
    </header>
    {folder && <nav className="breadcrumbs" aria-label="Your location"><div><Link to="/">All folders</Link><span aria-hidden="true">/</span>{pathname === folderPath ? <strong aria-current="page">{folder.name}</strong> : <Link to={folderPath}>{folder.name}</Link>}{inExam && <><span aria-hidden="true">/</span><strong aria-current="page">{exam ? `${exam.studentName ? exam.studentName + ' — ' : ''}${exam.title}` : 'Bulk check'}</strong></>}</div><Link className="button" to={pathname === folderPath ? '/' : folderPath}>{pathname === folderPath ? 'Back to all folders' : 'Back to exam list'}</Link></nav>}
    {folder && inExam && <nav className="workflow" aria-label="Exam workflow"><Link to={folderPath}><span>1</span>Exam list</Link><Link className={!reviewing ? 'active' : ''} aria-current={!reviewing ? 'step' : undefined} to={exam ? `${folderPath}/exams/${exam.id}/check` : pathname + search}><span>2</span>Criteria</Link>{exam?.status === 'completed' ? <Link className={reviewing ? 'active' : ''} aria-current={reviewing ? 'step' : undefined} to={`${folderPath}/exams/${exam.id}/review`}><span>3</span>Review</Link> : <span className="disabled" aria-disabled="true"><span>3</span>Review</span>}</nav>}
  </>;
}
