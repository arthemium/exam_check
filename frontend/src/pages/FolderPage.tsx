import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { useApp } from '../appContext';
import { isChecking } from '../criteria';
import type { Exam } from '../types';
import { NameDialog } from '../components/NameDialog';
import { DeleteDialog } from '../components/DeleteDialog';

export function examLabel(exam: Exam) { return exam.studentName ? `${exam.studentName} — ${exam.title}` : exam.title; }
function examStatus(exam: Exam, demo: boolean) {
  if (exam.status === 'completed') return `${demo && exam.source === 'upload' ? 'Demo checked' : 'Checked'}`;
  return { queued: 'Queued', processing: 'Checking', failed: 'Check failed', uploaded: 'Check exam' }[exam.status];
}
function ExamProgress({ exam }: { exam: Exam }) {
  if (!isChecking(exam.status) && exam.progress === undefined) return null;
  const stage = exam.status === 'completed' ? 'Complete' : exam.status === 'failed' ? 'Check failed' : exam.status === 'queued' ? 'Queued' : exam.stage === 'transcribing' ? 'Transcribing' : exam.stage === 'assessing' ? 'Assessing' : exam.stage === 'finalizing' ? 'Finishing' : 'Checking';
  return <div className="exam-progress" title="Progress shows completed processing stages, not time remaining."><div><span>{stage}</span><span>{exam.progress !== undefined ? `${Math.round(exam.progress)}%` : 'In progress'}</span></div><progress aria-label={`Checking ${examLabel(exam)}`} max={100} value={exam.progress} /></div>;
}

export function FolderPage() {
  const { folderId = '' } = useParams();
  const { data, repository } = useApp();
  const navigate = useNavigate();
  const location = useLocation();
  const folder = data.workspace.folders.find(item => item.id === folderId);
  const exams = data.workspace.exams.filter(exam => exam.folderId === folderId);
  const [selected, setSelected] = useState<string[]>([]);
  const [editor, setEditor] = useState<Exam | null>(null);
  const [deleting, setDeleting] = useState<Exam | null>(null);
  const [uploading, setUploading] = useState('');
  const uploader = useRef<HTMLInputElement>(null);
  const camera = useRef<HTMLInputElement>(null);
  const selectAll = useRef<HTMLInputElement>(null);
  const uploadGeneration = useRef(0);
  const available = exams.filter(exam => !isChecking(exam.status));
  const selectedIds = selected.filter(id => available.some(exam => exam.id === id));
  const batchIds = (location.state as { batchIds?: string[] } | null)?.batchIds || [];
  const batch = exams.filter(exam => batchIds.includes(exam.id));
  const complete = batch.filter(exam => exam.status === 'completed').length;
  const failed = batch.filter(exam => exam.status === 'failed').length;
  useEffect(() => {
    setSelected([]);
    setUploading('');
    setEditor(null);
    setDeleting(null);
    return () => { uploadGeneration.current += 1; };
  }, [folderId]);
  useEffect(() => { if (selectAll.current) selectAll.current.indeterminate = selectedIds.length > 0 && selectedIds.length < available.length; }, [selectedIds.length, available.length]);

  async function upload(event: ChangeEvent<HTMLInputElement>) {
    const files = [...(event.target.files || [])];
    event.target.value = '';
    if (!files.length || data.busy) return;
    if (files.length > 10) { data.setError('Choose up to 10 photos per upload.'); return; }
    const generation = uploadGeneration.current;
    await data.run(async () => {
      const failures: string[] = [];
      try {
        for (const [index, file] of files.entries()) {
          if (uploadGeneration.current !== generation) break;
          setUploading(`Adding photo ${index + 1} of ${files.length}…`);
          try { await repository.upload(folderId, file); }
          catch (failure) {
            if ((failure as { status?: number })?.status === 401) throw failure;
            failures.push(`${file.name}: ${failure instanceof Error ? failure.message : 'Upload failed'}`);
            continue;
          }
          await data.refresh().catch(() => undefined);
        }
      } finally {
        if (uploadGeneration.current === generation) setUploading('');
      }
      if (failures.length) throw new Error(failures.join(' '));
    });
  }
  function toggle(id: string) { setSelected(ids => ids.includes(id) ? ids.filter(value => value !== id) : [...ids, id]); }
  if (!folder) return <div className="panel empty-state"><h1>Folder not found</h1><Link to="/">Back to all folders</Link></div>;
  return <section>
    <div className="screen-heading"><div><span className="eyebrow">EXAM FOLDER</span><h1>{folder.name}</h1><p className="muted">{exams.length} exams · Open one exam or select several to check.</p></div><div className="heading-actions"><button className="button primary" disabled={data.busy} onClick={() => uploader.current?.click()}>Upload exams</button><button className="button" disabled={data.busy} onClick={() => camera.current?.click()}>Take photo</button></div></div>
    <input ref={uploader} aria-label="Upload exams" type="file" accept="image/jpeg,image/png,image/webp" multiple hidden onChange={event => void upload(event)} /><input ref={camera} aria-label="Take exam photo" type="file" accept="image/jpeg,image/png,image/webp" capture="environment" hidden onChange={event => void upload(event)} />
    {uploading && <p role="status" className="notice">{uploading}</p>}
    {batch.length > 0 && <div className="panel batch-summary"><div><h2>{batch.length > 1 ? 'Bulk check progress' : 'Check progress'}</h2><p role="status">{complete} of {batch.length} exams complete{failed > 0 && ` · ${failed} failed`}</p>{failed > 0 && <p className="helper">Open a failed exam below to retry its check.</p>}{repository.capabilities.demo && <p className="helper">Checking progress is simulated in demo mode.</p>}</div><button className="button primary" disabled={complete === 0} onClick={() => navigate(`/folders/${folderId}/exams/${batch.find(exam => exam.status === 'completed')!.id}/review`)}>View results</button></div>}
    {exams.length ? <section className="panel"><div className="panel-header"><h2>Student / exam</h2><label className="select-all"><input ref={selectAll} type="checkbox" aria-label="Select all exams" disabled={!available.length} checked={available.length > 0 && selectedIds.length === available.length} onChange={event => setSelected(event.target.checked ? available.map(exam => exam.id) : [])} />Select all</label></div>
      {selectedIds.length > 0 && <div className="selection-toolbar"><span>{selectedIds.length} selected{selectedIds.length === 1 ? ' · Select one more for bulk check' : ''}</span><div className="inline-actions"><button className="button" onClick={() => setSelected([])}>Clear selection</button><button className="button primary" disabled={selectedIds.length < 2 || data.busy} onClick={() => navigate(`/folders/${folderId}/check?ids=${selectedIds.join(',')}`)}>Bulk check</button></div></div>}
      <ul className="item-list" aria-label="Exams">{exams.map(exam => <li className="item-row exam-row" key={exam.id}><input className="exam-selection" type="checkbox" aria-label={`Select exam: ${examLabel(exam)}`} checked={selectedIds.includes(exam.id)} disabled={isChecking(exam.status)} onChange={() => toggle(exam.id)} /><button className="item-link" disabled={isChecking(exam.status)} onClick={() => navigate(`/folders/${folderId}/exams/${exam.id}/${exam.status === 'completed' ? 'review' : 'check'}`)}><span className="item-copy"><strong>{exam.studentName || exam.title}</strong><small>{exam.studentName ? exam.title + ' · ' : ''}{exam.source === 'sample' ? 'Sample exam' : 'Uploaded photo'}</small></span><span className={`status ${exam.status === 'completed' ? 'checked' : ''}`}>{exam.status === 'completed' && <span aria-hidden="true">✓ </span>}{examStatus(exam, repository.capabilities.demo)}</span></button><div className="row-actions"><button className="button" disabled={data.busy} onClick={() => setEditor(exam)}>Rename</button>{repository.capabilities.delete && <button className="button danger" disabled={data.busy} onClick={() => setDeleting(exam)}>Delete</button>}</div><ExamProgress exam={exam} />{exam.error && <p className="row-error" role="status">{exam.error}</p>}</li>)}</ul>
    </section> : <div className="panel empty-state"><h2>Your folder is ready</h2><p>Upload exams or take a photo. Each photo becomes a separate exam in this list.</p></div>}
    <p className="helper page-note">Click an exam for an individual check. Select two or more rows for a bulk check. One photo per exam.</p>
    {repository.capabilities.demo && <><button className="button quiet" disabled={data.busy} onClick={() => void data.run(() => repository.addSamples(folderId))}>Try 3 sample exams</button><p className="helper">Photo previews stay in this browser. Real-photo OCR and grading are not connected in demo mode.</p></>}
    {editor && <NameDialog title="Rename exam" exam name={editor.title} studentName={editor.studentName} busy={data.busy} onClose={() => setEditor(null)} onSave={(title, studentName) => data.run(() => repository.saveExam(editor.id, { title, studentName }))} />}
    {deleting && <DeleteDialog label="exam" description={`“${examLabel(deleting)}” and its feedback will be removed.`} busy={data.busy} onClose={() => setDeleting(null)} onDelete={() => data.run(() => repository.deleteExam(deleting.id))} />}
  </section>;
}
