import { useState } from 'react';
import type { Capabilities, Check, Criteria, Exam } from '../types';
import { basicChecks, checkOptions, criteriaError, defaultCriteria } from '../criteria';
import { PhotoViewer } from './PhotoViewer';
import { TaskInstructions } from './TaskInstructions';

export interface CriteriaScreenProps {
  exams: Exam[];
  capabilities: Capabilities;
  busy: boolean;
  onSave: (criteria: Criteria, check: boolean, identity?: { title: string; studentName: string }) => Promise<void>;
  onCancel: () => void;
}

function initialCriteria(exams: Exam[]): Criteria {
  const criteria = structuredClone(exams[0]?.criteria || defaultCriteria());
  return exams.length > 1 ? { ...criteria, taskText: '', taskSource: '', taskPhoto: undefined } : criteria;
}

function CriteriaOptions({ criteria, advanced, disabled, onChange }: { criteria: Criteria; advanced: boolean; disabled: boolean; onChange: (patch: Partial<Criteria>) => void }) {
  function toggle(check: Check, selected: boolean) {
    onChange({ checks: selected ? [...criteria.checks, check] : criteria.checks.filter(value => value !== check) });
  }
  return <>
    <label className="field">Assessment criteria<select value={criteria.assessment} disabled={disabled} onChange={event => {
      const assessment = event.target.value as Criteria['assessment'];
      onChange({ assessment, ...(assessment === 'cae' && !criteria.minWords && !criteria.maxWords ? { minWords: '220', maxWords: '260' } : {}) });
    }}><option value="general">General writing</option><option value="cae" disabled={!advanced}>Cambridge C1 Advanced (CAE) Essay</option></select></label>
    {criteria.assessment === 'cae' && <div className="rubric"><h3>CAE assessment criteria</h3><dl>
      <dt>Content</dt><dd>Task completion, relevance, and how fully the reader is informed.</dd>
      <dt>Communicative Achievement</dt><dd>Format, register, style, and purpose.</dd>
      <dt>Organisation</dt><dd>Paragraphs, coherence, and linking ideas.</dd>
      <dt>Language</dt><dd>Vocabulary and grammar: range and accuracy.</dd>
    </dl></div>}
    {!advanced && <p className="notice">Additional assessment criteria are not available on this server yet.</p>}
    <div className="option-list">{checkOptions.map(option => <label key={option.value} className="option" data-check={option.value}><input type="checkbox" checked={criteria.checks.includes(option.value)} disabled={disabled || (!advanced && !basicChecks.includes(option.value))} onChange={event => toggle(option.value, event.target.checked)} /><span><strong>{option.label}</strong><small>{option.description}</small></span></label>)}</div>
    <label className="field">Custom criteria <span className="muted">Optional</span><textarea rows={3} maxLength={1200} disabled={disabled || !advanced} value={criteria.customCriteria} onChange={event => onChange({ customCriteria: event.target.value })} placeholder="One criterion per line" /></label>
    <fieldset className="word-limits" disabled={disabled || !advanced}><legend>Word limit <span className="muted">Optional</span></legend><div className="identity-fields">
      <label className="field">Minimum words<input inputMode="numeric" maxLength={5} value={criteria.minWords} onChange={event => onChange({ minWords: event.target.value })} placeholder="No minimum" /></label>
      <label className="field">Maximum words<input inputMode="numeric" maxLength={5} value={criteria.maxWords} onChange={event => onChange({ maxWords: event.target.value })} placeholder="No maximum" /></label>
    </div><p className="helper">Count the student's answer only. The CAE range is 220–260 words.</p></fieldset>
  </>;
}

function SelectedExams({ exams }: { exams: Exam[] }) {
  return <section className="panel" aria-label="Selected exams"><div className="panel-header"><h2>Selected exams</h2><span className="muted">{exams.length} selected</span></div><ul className="selected-exams">{exams.map(exam => <li key={exam.id}>
    {exam.photos[0] ? <img src={exam.photos[0].url} alt="" /> : <span className="sample-thumbnail">Sample</span>}
    <div><strong>{exam.studentName || exam.title}</strong><p>{exam.studentName ? exam.title : 'Student not named'}</p><small>{exam.criteria.taskText ? 'Individual task instructions saved' : 'No task instructions yet'}</small></div>
  </li>)}</ul></section>;
}

export function CriteriaScreen({ exams, capabilities, busy, onSave, onCancel }: CriteriaScreenProps) {
  const [criteria, setCriteria] = useState(() => initialCriteria(exams));
  const [title, setTitle] = useState(exams[0]?.title || '');
  const [studentName, setStudentName] = useState(exams[0]?.studentName || '');
  const [submitting, setSubmitting] = useState(false);
  const [photoBusy, setPhotoBusy] = useState(false);
  const [error, setError] = useState('');
  const bulk = exams.length > 1;
  const disabled = busy || submitting || photoBusy;
  const limitError = criteriaError({ ...criteria, checks: ['grammar'] });
  const checkError = criteriaError(criteria);
  const update = (patch: Partial<Criteria>) => setCriteria(value => ({ ...value, ...patch }));
  async function save(check: boolean) {
    if (disabled) return;
    const problem = (!bulk && !title.trim() ? 'Enter an exam title.' : '') || (check ? checkError : limitError);
    if (problem) { setError(problem); return; }
    setError('');
    setSubmitting(true);
    try { await onSave(criteria, check, bulk ? undefined : { title: title.trim(), studentName: studentName.trim() }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'The exam could not be saved.'); }
    finally { setSubmitting(false); }
  }
  if (!exams.length) return <section className="panel empty-state"><h1>No exams selected</h1><button className="button" onClick={onCancel}>Back to exam list</button></section>;
  return <>
    <div className="screen-heading"><div><p className="eyebrow">CHOOSE CRITERIA</p><h1>{bulk ? 'Check selected exams' : 'Check exam'}</h1><p className="muted">{bulk ? 'These criteria apply only to the selected exams.' : 'Review the photo and choose what to check.'}</p></div><button className="button" onClick={onCancel}>Back to exam list</button></div>
    {!bulk && <div className="identity-fields"><label className="field">Student name<input maxLength={120} disabled={disabled} value={studentName} onChange={event => setStudentName(event.target.value)} placeholder="e.g. Anna Smith" /></label><label className="field">Exam title<input maxLength={120} required disabled={disabled} value={title} onChange={event => setTitle(event.target.value)} /></label></div>}
    <div className="criteria-layout"><div className="source-stack">
      {bulk ? <SelectedExams exams={exams} /> : <PhotoViewer key={exams[0].id} photos={exams[0].photos} exam={exams[0]} compact />}
      <label className="option detection-option"><input type="checkbox" checked={criteria.detectTask} disabled={disabled || !capabilities.advancedCriteria} onChange={event => update({ detectTask: event.target.checked })} /><span><strong>Find the task on the essay page</strong><small>Keep the instructions separate from the student's answer.</small></span></label>
      <TaskInstructions criteria={criteria} capabilities={capabilities} bulk={bulk} disabled={disabled} onChange={update} onBusyChange={setPhotoBusy} />
    </div><section className="panel criteria-panel" aria-label="Checking options"><div className="panel-header"><h2>What should we check?</h2></div><div className="panel-body">
      <CriteriaOptions criteria={criteria} advanced={capabilities.advancedCriteria} disabled={disabled} onChange={update} />
      {(error || limitError) && <p className="form-error" role="alert">{error || limitError}</p>}
      <div className="criteria-actions"><button type="button" className="button primary" disabled={disabled || !!checkError || (!bulk && !title.trim())} onClick={() => void save(true)}>{submitting ? 'Saving…' : bulk ? `Check ${exams.length} selected exams` : 'Check exam'}</button><button type="button" className="button" disabled={disabled || !!limitError || (!bulk && !title.trim())} onClick={() => void save(false)}>{bulk ? 'Save criteria for selected exams' : 'Save exam'}</button></div>
      {checkError && !limitError && <p className="helper">{checkError}</p>}
      {capabilities.demo && <p className="helper">Preview checking is simulated. Uploaded photos do not receive invented transcriptions.</p>}
    </div></section></div>
  </>;
}
