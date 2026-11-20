import { useEffect, useRef, useState } from 'react';
import type { Capabilities, Check, Exam, Issue, Paragraph } from '../types';
import { checkOptions, isChecking } from '../criteria';
import { PhotoViewer } from './PhotoViewer';
import { countWords, segmentParagraph, wordCountMessage } from './transcript';

export interface ReviewScreenProps {
  exam: Exam;
  exams: Exam[];
  capabilities: Capabilities;
  onOpen: (id: string) => void;
  onEdit: () => void;
  onBack: () => void;
}

const categoryLabel = (issue: Issue) => checkOptions.find(option => option.value === issue.category)?.label || issue.category;
type CheckFilter = Check | 'all';

function filteredIssues(exam: Exam, category: CheckFilter): Issue[] {
  return (exam.result?.issues || []).filter(issue => category === 'all' || issue.category === category);
}

function CheckFilters({ exam, category, onSelect }: { exam: Exam; category: CheckFilter; onSelect: (category: CheckFilter) => void }) {
  const options = checkOptions.filter(option => exam.criteria.checks.includes(option.value) || exam.result?.issues.some(issue => issue.category === option.value));
  return <div className="check-filters" role="group" aria-label="Filter feedback by check">
    <button type="button" className={`button ${category === 'all' ? 'active' : ''}`} aria-pressed={category === 'all'} disabled={!exam.result} onClick={() => onSelect('all')}>All feedback{exam.result && <span className="tag">{exam.result.issues.length}</span>}</button>
    {options.map(option => <button type="button" key={option.value} data-check={option.value} className={`button ${category === option.value ? 'active' : ''}`} aria-pressed={category === option.value} disabled={!exam.result} onClick={() => onSelect(option.value)}>{option.label}<span className="tag">{exam.result ? filteredIssues(exam, option.value).length : 'Pending'}</span></button>)}
  </div>;
}

function ExamNavigation({ exam, exams, onOpen }: Pick<ReviewScreenProps, 'exam' | 'exams' | 'onOpen'>) {
  const index = exams.findIndex(item => item.id === exam.id);
  const previous = exams[index - 1];
  const next = exams[index + 1];
  if (exams.length < 2) return null;
  return <nav className="panel review-navigation" aria-label="Browse exams"><div className="review-paging">
    <button className="button" disabled={!previous || isChecking(previous.status)} onClick={() => previous && onOpen(previous.id)}>Previous exam</button><span role="status">Exam {index + 1} of {exams.length}</span><button className="button" disabled={!next || isChecking(next.status)} onClick={() => next && onOpen(next.id)}>Next exam</button>
  </div><div className="exam-tabs">{exams.map((item, position) => <button key={item.id} className={`button ${item.id === exam.id ? 'active' : ''}`} aria-current={item.id === exam.id ? 'page' : undefined} disabled={isChecking(item.status)} onClick={() => onOpen(item.id)}>Exam {position + 1}{item.studentName ? ` · ${item.studentName}` : ''}</button>)}</div></nav>;
}

function TranscriptParagraph({ paragraph, issues, selectedId, onSelect }: { paragraph: Paragraph; issues: Issue[]; selectedId: string; onSelect: (id: string) => void }) {
  const parts = segmentParagraph(paragraph, issues);
  return <p>{parts.map(part => {
    if (part.uncertain) return <span key={part.start} className="uncertainty" title="Uncertain handwriting — review against the photo">{part.text || '[unreadable]'}</span>;
    if (!part.issueIds.length) return <span key={part.start}>{part.text}</span>;
    const issueId = part.issueIds.includes(selectedId) ? selectedId : part.issueIds[0];
    const issue = issues.find(item => item.id === issueId)!;
    return <button key={part.start} type="button" data-check={issue.category} className={`text-mark mark-${issue.category} ${part.issueIds.includes(selectedId) ? 'selected' : ''}`} aria-pressed={part.issueIds.includes(selectedId)} aria-label={`${categoryLabel(issue)} feedback for ${part.text}`} onClick={() => onSelect(issue.id)}>{part.text}</button>;
  })}</p>;
}

function Transcript({ exam, issues, selectedId, onSelect, demo }: { exam: Exam; issues: Issue[]; selectedId: string; onSelect: (id: string) => void; demo: boolean }) {
  const result = exam.result;
  return <section className="panel transcript" aria-label="Exam transcription"><div className="panel-header"><h2>Transcription</h2><span className="muted">Student's wording preserved</span></div>
    {!result ? <div className="empty-state"><h3>{exam.status === 'failed' ? 'Checking did not finish' : 'Transcription pending'}</h3><p>{exam.error || (demo ? 'Your photo is ready. Real-photo text extraction is not connected in this preview.' : 'The transcription will appear here when checking finishes.')}</p></div> : <>
      {result.needsTranscriptionReview && <p className="notice">Some handwriting is uncertain. Compare the marked text with the photo.</p>}
      <div className="feedback-legend" aria-label="Feedback styles">{checkOptions.filter(option => issues.some(issue => issue.category === option.value && issue.scope === 'span')).map(option => <span key={option.value} className="legend-check" data-check={option.value}>{option.label}</span>)}<span>Uncertain handwriting: dotted underline</span></div>
      <article className="essay"><h3>{exam.title}</h3>{result.paragraphs.map(paragraph => <TranscriptParagraph key={paragraph.id} paragraph={paragraph} issues={issues} selectedId={selectedId} onSelect={onSelect} />)}</article>
      <p className="helper panel-footer">Select a marked word to see its feedback.</p>
    </>}
  </section>;
}

function Feedback({ exam, category, selectedId, onSelect, onFilter }: { exam: Exam; category: CheckFilter; selectedId: string; onSelect: (id: string) => void; onFilter: (category: CheckFilter) => void }) {
  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const list = listRef.current;
    const selected = list?.querySelector('.feedback-card.selected')?.parentElement;
    if (!list || !selected) return;
    const top = selected.offsetTop;
    if (top < list.scrollTop || top + selected.offsetHeight > list.scrollTop + list.clientHeight) list.scrollTop = top;
  }, [selectedId, category]);
  const result = exam.result;
  const issues = filteredIssues(exam, category);
  const label = checkOptions.find(option => option.value === category)?.label;
  return <section className="panel feedback" aria-label="Review feedback"><div className="panel-header"><h2>Feedback</h2>{result && <span className="tag">{issues.length} finding{issues.length === 1 ? '' : 's'}</span>}</div>
    <CheckFilters exam={exam} category={category} onSelect={onFilter} />
    {result && <p className="helper feedback-status" role="status">{label ? `${label} feedback` : 'All selected checks'} · Select a finding to highlight its wording below.</p>}
    {!result ? <div className="empty-state"><h3>{exam.status === 'failed' ? 'Try checking again' : 'Criteria saved'}</h3><p>Feedback will appear here after checking. No findings have been generated for this photo.</p></div> : !issues.length ? <div className="empty-state"><h3>{label ? `No ${label.toLowerCase()} findings` : 'No findings for the selected checks'}</h3><p>The original wording is preserved in the transcription.</p></div> : <div className="feedback-list" ref={listRef}>{issues.map(issue => <div key={issue.id} data-check={issue.category}>
      <button type="button" className={`feedback-card ${selectedId === issue.id ? 'selected' : ''}`} aria-pressed={selectedId === issue.id} onClick={() => onSelect(issue.id)}><span className="eyebrow">{categoryLabel(issue)}{issue.scope !== 'span' ? ' · Suggestion' : ''}</span>{issue.quote && <span className="issue-quote">{issue.quote}</span>}<strong>{issue.suggestion}</strong></button>
      {selectedId === issue.id && <div className="feedback-detail" aria-live="polite"><h3>{issue.scope === 'span' ? 'Why this change?' : 'A suggestion to consider'}</h3><p>{issue.explanation}</p></div>}
    </div>)}</div>}
    {!!result?.warnings?.length && <div className="notice"><h3>Review notes</h3><ul>{result.warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></div>}
    <AssessmentFeedback exam={exam} />
  </section>;
}

function AssessmentFeedback({ exam }: { exam: Exam }) {
  const result = exam.result;
  return <>
    {!!result?.assessments?.length && <section className="assessment-feedback" aria-label="CAE assessment"><h3>CAE assessment</h3><p className="helper">Estimated bands for teacher review, not an official Cambridge score.</p><dl>{result.assessments.map(item => <div key={item.criterion}><dt>{item.criterion}<span className="tag">{item.score === null ? 'Not assessable' : `${item.score} / 5`}</span></dt><dd>{item.feedback}</dd></div>)}</dl></section>}
    {!!result?.customAssessments?.length && <section className="assessment-feedback" aria-label="Custom criteria assessment"><h3>Custom criteria</h3><dl>{result.customAssessments.map((item, index) => <div key={index}><dt>{item.criterion}</dt><dd>{item.feedback}</dd></div>)}</dl></section>}
  </>;
}

function resultWordLimit(exam: Exam, count: number): string {
  const limit = exam.result?.wordLimit;
  if (!limit) return wordCountMessage(count, exam.criteria);
  if (limit.status === 'not_set') return 'No word limit set';
  if (limit.status === 'below') return `Below the ${limit.minimum}-word minimum`;
  if (limit.status === 'above') return `Above the ${limit.maximum}-word maximum`;
  return 'Within the word limit';
}

function CriteriaSummary({ exam, demo }: { exam: Exam; demo: boolean }) {
  const { criteria } = exam;
  const previewUnavailable = demo && (criteria.assessment === 'cae' || !!criteria.customCriteria.trim() || criteria.checks.some(check => !['grammar', 'spelling', 'punctuation', 'structure'].includes(check)));
  if (criteria.assessment !== 'cae' && !criteria.customCriteria.trim() && !previewUnavailable) return null;
  return <section className="panel criterion-summary" aria-label="Selected assessment criteria"><div className="panel-header"><h2>{criteria.assessment === 'cae' ? 'Cambridge C1 Advanced (CAE) criteria' : 'Assessment criteria'}</h2></div><div className="panel-body">
    {criteria.assessment === 'cae' && <p>Content · Communicative Achievement · Organisation · Language</p>}
    {criteria.customCriteria.trim() && <><h3>Custom criteria</h3><ul>{criteria.customCriteria.split('\n').filter(line => line.trim()).map((line, index) => <li key={index}>{line}</li>)}</ul></>}
    {previewUnavailable && <p className="helper">These criteria are saved. This preview does not generate an assessment or score for them.</p>}
  </div></section>;
}

export function ReviewScreen({ exam, exams, capabilities, onOpen, onEdit, onBack }: ReviewScreenProps) {
  const [selectedId, setSelectedId] = useState(exam.result?.issues[0]?.id || '');
  const [category, setCategory] = useState<CheckFilter>('all');
  useEffect(() => { setCategory('all'); setSelectedId(exam.result?.issues[0]?.id || ''); }, [exam.id]);
  useEffect(() => {
    const issues = filteredIssues(exam, category);
    setSelectedId(value => issues.some(issue => issue.id === value) ? value : issues[0]?.id || '');
  }, [exam.result, category]);
  function selectCategory(value: CheckFilter) {
    setCategory(value);
    setSelectedId(filteredIssues(exam, value)[0]?.id || '');
  }
  const wordCount = exam.result ? exam.result.wordCount ?? countWords(exam.result.paragraphs) : null;
  const task = exam.result?.task ?? { text: exam.criteria.taskText, source: exam.criteria.taskSource };
  const label = exam.studentName ? `${exam.studentName} — ${exam.title}` : exam.title;
  const status = isChecking(exam.status) ? 'Checking' : exam.status === 'failed' ? 'Check failed' : exam.status === 'completed' ? (capabilities.demo && exam.source === 'upload' ? 'Demo checked' : 'Checked') : 'Not checked';
  return <>
    <div className="screen-heading"><div><p className="eyebrow">EXAM REVIEW</p><h1>{label}</h1><p className="muted">{status} · {exam.source === 'sample' ? 'Sample exam' : `${exam.photos.length} photo${exam.photos.length === 1 ? '' : 's'}`}</p></div><div className="heading-actions"><button className="button" onClick={onBack}>Back to exam list</button><button className="button" disabled={isChecking(exam.status)} onClick={onEdit}>Edit task &amp; criteria</button></div></div>
    <ExamNavigation exam={exam} exams={exams} onOpen={onOpen} />
    <div className="review-layout">
    <div className="review-main">
      <Feedback exam={exam} category={category} selectedId={selectedId} onSelect={setSelectedId} onFilter={selectCategory} />
      <Transcript exam={exam} issues={filteredIssues(exam, category)} selectedId={selectedId} onSelect={setSelectedId} demo={capabilities.demo} />
    </div>
    <aside className="review-sidebar" aria-label="Original exam and task details">
    <PhotoViewer key={exam.id} photos={exam.photos} exam={exam} compact />
    <section className="panel review-task" aria-label="Task instructions used"><div className="panel-header"><h2>Task instructions</h2><span className="muted">{task.source === 'essay' ? 'From essay page' : task.source === 'photo' ? 'From task photo' : task.text ? 'Reviewed / typed instructions' : 'Not supplied'}</span></div><p className="task-text">{task.text || 'No task supplied. Task completion cannot be assessed without instructions.'}</p></section>
    <section className="panel word-summary" aria-label="Word count"><div><strong className="metric">{wordCount === null ? 'Word count pending' : `${wordCount} words`}</strong><p className="helper">Student answer only · Title and task instructions excluded{exam.result?.needsTranscriptionReview && exam.result.wordCount !== undefined ? ' · Approximate while handwriting is uncertain' : ''}</p></div><p>{wordCount === null ? 'Available after transcription' : resultWordLimit(exam, wordCount)}</p></section>
    <CriteriaSummary exam={exam} demo={capabilities.demo} />
    </aside>
    </div>
  </>;
}
