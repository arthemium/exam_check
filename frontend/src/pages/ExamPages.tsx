import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useApp } from '../appContext';
import { isChecking } from '../criteria';
import { CriteriaScreen } from '../components/CriteriaScreen';
import { ReviewScreen } from '../components/ReviewScreen';
import type { Criteria, Exam } from '../types';

function useExamDetails(ids: string[]) {
  const { repository, data } = useApp();
  const key = ids.join(',');
  const [readyKey, setReadyKey] = useState('');
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setError('');
    void Promise.all(ids.map(id => repository.getExam(id))).then(async () => {
      if (cancelled) return;
      await data.refresh();
      if (!cancelled) setReadyKey(key);
    }).catch(failure => { if (!cancelled) { setError(failure instanceof Error ? failure.message : 'Exam could not be loaded.'); setReadyKey(key); } });
    return () => { cancelled = true; };
  }, [repository, key, data.refresh, attempt]);
  function retry() { setReadyKey(''); setAttempt(value => value + 1); }
  return { loading: readyKey !== key, error, retry };
}

function MissingExam({ folderId, message = 'Exam not found', onRetry }: { folderId: string; message?: string; onRetry?: () => void }) {
  return <div className="panel empty-state"><h1>{message}</h1>{onRetry && <button className="button" onClick={onRetry}>Try again</button>}<Link className="button" to={`/folders/${folderId}`}>Back to exam list</Link></div>;
}

function criteriaForExam(criteria: Criteria, exam: Exam, bulk: boolean): Criteria {
  if (!bulk || criteria.taskText.trim() || criteria.taskPhoto) return criteria;
  const { taskText, taskSource, taskPhoto } = exam.criteria;
  return { ...criteria, taskText, taskSource, taskPhoto };
}

export function CheckPage() {
  const { folderId = '', examId } = useParams();
  const [search] = useSearchParams();
  const navigate = useNavigate();
  const { data, repository } = useApp();
  const ids = examId ? [examId] : [...new Set((search.get('ids') || '').split(',').filter(Boolean))];
  const details = useExamDetails(ids);
  const exams = ids.map(id => data.workspace.exams.find(exam => exam.id === id && exam.folderId === folderId)).filter((exam): exam is Exam => !!exam);
  if (details.loading) return <p className="loading" role="status">Loading exams…</p>;
  if (details.error) return <MissingExam folderId={folderId} message={details.error} onRetry={details.retry} />;
  if (exams.length !== ids.length || !exams.length || (!examId && exams.length < 2)) return <MissingExam folderId={folderId} message="Select exams from your folder first" />;
  if (exams.some(exam => isChecking(exam.status))) return <MissingExam folderId={folderId} message="Checking is in progress" />;

  async function save(criteria: Criteria, check: boolean, identity?: { title: string; studentName: string }) {
    const success = await data.run(async () => {
      if (identity && examId) await repository.saveExam(examId, identity);
      if (check) await repository.checkExams(ids, criteria);
      else for (const exam of exams) await repository.saveExam(exam.id, { criteria: criteriaForExam(criteria, exam, exams.length > 1) });
    });
    if (success) navigate(`/folders/${folderId}`, { state: check ? { batchIds: ids } : null });
    else throw new Error('The changes could not be saved. See the message above and try again.');
  }
  return <CriteriaScreen key={ids.join(',')} exams={exams} capabilities={repository.capabilities} busy={data.busy} onSave={save} onCancel={() => navigate(`/folders/${folderId}`)} />;
}

export function ReviewPage() {
  const { folderId = '', examId = '' } = useParams();
  const navigate = useNavigate();
  const { data, repository } = useApp();
  const details = useExamDetails([examId]);
  const exam = data.workspace.exams.find(item => item.id === examId && item.folderId === folderId);
  if (details.loading) return <p className="loading" role="status">Loading review…</p>;
  if (details.error || !exam) return <MissingExam folderId={folderId} message={details.error || 'Exam not found'} onRetry={details.error ? details.retry : undefined} />;
  const exams = data.workspace.exams.filter(item => item.folderId === folderId && (!exam.batchId || item.batchId === exam.batchId));
  return <ReviewScreen exam={exam} exams={exams} capabilities={repository.capabilities} onOpen={id => navigate(`/folders/${folderId}/exams/${id}/review`)} onEdit={() => navigate(`/folders/${folderId}/exams/${examId}/check`)} onBack={() => navigate(`/folders/${folderId}`)} />;
}
