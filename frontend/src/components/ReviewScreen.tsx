import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import type { Capabilities, Check, Exam, Issue, Paragraph } from '../types';
import { checkOptions, isChecking } from '../criteria';
import { PhotoViewer } from './PhotoViewer';
import { TranscriptHighlight } from './TranscriptHighlight';
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
type FeedbackTab = 'text' | 'essay';

function tabIssues(exam: Exam, tab: FeedbackTab): Issue[] {
  return (exam.result?.issues || []).filter(issue => (issue.scope === 'span') === (tab === 'text'));
}

function initialFeedbackTab(exam: Exam): FeedbackTab {
  const result = exam.result;
  return !tabIssues(exam, 'text').length && (result?.issues.length || result?.assessments?.length || result?.customAssessments?.length || result?.warnings?.length) ? 'essay' : 'text';
}

function filteredIssues(exam: Exam, category: CheckFilter, tab: FeedbackTab): Issue[] {
  return tabIssues(exam, tab).filter(issue => category === 'all' || issue.category === category);
}

function CheckFilters({ exam, category, tab, onSelect }: { exam: Exam; category: CheckFilter; tab: FeedbackTab; onSelect: (category: CheckFilter) => void }) {
  const issues = tabIssues(exam, tab);
  const options = checkOptions.filter(option => exam.result ? issues.some(issue => issue.category === option.value) : exam.criteria.checks.includes(option.value));
  if (exam.result && !issues.length) return null;
  return <div className="check-filters" role="group" aria-label="Filter feedback by check">
    <button type="button" className={`button ${category === 'all' ? 'active' : ''}`} aria-pressed={category === 'all'} disabled={!exam.result} onClick={() => onSelect('all')}>All feedback{exam.result && <span className="tag">{issues.length}</span>}</button>
    {options.map(option => <button type="button" key={option.value} data-check={option.value} className={`button ${category === option.value ? 'active' : ''}`} aria-pressed={category === option.value} disabled={!exam.result} onClick={() => onSelect(option.value)}>{option.label}<span className="tag">{exam.result ? filteredIssues(exam, option.value, tab).length : 'Pending'}</span></button>)}
  </div>;
}

function FeedbackTabs({ id, tab, onSelect }: { id: string; tab: FeedbackTab; onSelect: (tab: FeedbackTab) => void }) {
  const tabs = [{ key: 'text', label: 'Text corrections' }, { key: 'essay', label: 'Essay feedback' }] as const;
  function navigate(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? 1 : 1 - index;
    onSelect(tabs[next].key);
    event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
  }
  return <div className="feedback-tabs" role="tablist" aria-label="Feedback type">{tabs.map((item, index) =>
    <button type="button" key={item.key} id={`${id}-${item.key}`} role="tab" aria-selected={tab === item.key} aria-controls={`${id}-panel`} tabIndex={tab === item.key ? 0 : -1} onClick={() => onSelect(item.key)} onKeyDown={event => navigate(event, index)}>{item.label}</button>
  )}</div>;
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

function TranscriptParagraph({ paragraph, index, anchorId, issues, selectedId, onSelect }: { paragraph: Paragraph; index: number; anchorId: string; issues: Issue[]; selectedId: string; onSelect: (id: string) => void }) {
  const parts = segmentParagraph(paragraph, issues);
  const selected = issues.find(issue => issue.id === selectedId && issue.scope === 'paragraph' && issue.paragraphId === paragraph.id);
  return <div id={anchorId} tabIndex={-1} className={`transcript-paragraph ${selected ? 'paragraph-focus' : ''}`} data-check={selected?.category}>
    {selected && <span className="paragraph-location">Page {paragraph.pageNumber} · Paragraph {index + 1}</span>}
    <p>{parts.map(part => {
    if (part.uncertain) return <span key={part.start} className="uncertainty" title="Uncertain handwriting — review against the photo">{part.text || '[unreadable]'}</span>;
    if (!part.issueIds.length) return <span key={part.start}>{part.text}</span>;
    return <TranscriptHighlight key={part.start} text={part.text} issues={issues.filter(issue => part.issueIds.includes(issue.id))} selectedId={selectedId} onSelect={onSelect} />;
  })}</p></div>;
}

function Transcript({ exam, issues, selectedId, onSelect, demo, anchorPrefix }: { exam: Exam; issues: Issue[]; selectedId: string; onSelect: (id: string) => void; demo: boolean; anchorPrefix: string }) {
  const result = exam.result;
  return <section className="panel transcript" aria-label="Exam transcription"><div className="panel-header"><h2>Transcription</h2><span className="muted">Student's wording preserved</span></div>
    {!result ? <div className="empty-state"><h3>{exam.status === 'failed' ? 'Checking did not finish' : 'Transcription pending'}</h3><p>{exam.error || (demo ? 'Your photo is ready. Real-photo text extraction is not connected in this preview.' : 'The transcription will appear here when checking finishes.')}</p></div> : <>
      {result.needsTranscriptionReview && <p className="notice">Some handwriting is uncertain. Compare the marked text with the photo.</p>}
      <div className="feedback-legend" aria-label="Feedback styles">{checkOptions.filter(option => issues.some(issue => issue.category === option.value && issue.scope === 'span')).map(option => <span key={option.value} className="legend-check" data-check={option.value}>{option.label}</span>)}<span>Uncertain handwriting: dotted underline</span></div>
      <article className="essay"><h3>{exam.title}</h3>{result.paragraphs.map((paragraph, index) => <TranscriptParagraph key={paragraph.id} paragraph={paragraph} index={index} anchorId={`${anchorPrefix}-${paragraph.id}`} issues={issues} selectedId={selectedId} onSelect={onSelect} />)}</article>
      <p className="helper panel-footer">Hover over a marked word for suggestions. Select essay feedback to see its related paragraph.</p>
    </>}
  </section>;
}

function FeedbackLocation({ exam, issue, onLocate }: { exam: Exam; issue: Issue; onLocate: (paragraphId: string) => void }) {
  if (issue.scope === 'span') return null;
  const index = exam.result?.paragraphs.findIndex(paragraph => paragraph.id === issue.paragraphId) ?? -1;
  if (issue.scope !== 'paragraph' || index < 0) return <p className="helper">{issue.scope === 'document' ? 'Whole essay' : 'No paragraph location available'}</p>;
  const paragraph = exam.result!.paragraphs[index];
  return <div className="feedback-location"><span className="helper">Page {paragraph.pageNumber}</span><button type="button" className="button" onClick={() => onLocate(paragraph.id)}>Show paragraph {index + 1}</button></div>;
}

function Feedback({ exam, category, tab, selectedId, onSelect, onFilter, onTab, onLocate }: { exam: Exam; category: CheckFilter; tab: FeedbackTab; selectedId: string; onSelect: (id: string) => void; onFilter: (category: CheckFilter) => void; onTab: (tab: FeedbackTab) => void; onLocate: (paragraphId: string) => void }) {
  const listRef = useRef<HTMLDivElement>(null);
  const tabsId = useId();
  useEffect(() => { if (listRef.current) listRef.current.scrollTop = 0; }, [exam.id, category, tab]);
  useEffect(() => {
    const list = listRef.current;
    const selected = list?.querySelector('.feedback-card.selected')?.parentElement;
    if (!list || !selected) return;
    const top = selected.offsetTop;
    if (top < list.scrollTop || top + selected.offsetHeight > list.scrollTop + list.clientHeight) list.scrollTop = top;
  }, [selectedId, category, tab]);
  const result = exam.result;
  const issues = filteredIssues(exam, category, tab);
  const label = checkOptions.find(option => option.value === category)?.label;
  const hasAssessments = !!(result?.assessments?.length || result?.customAssessments?.length);
  return <section className="panel feedback" aria-label="Review feedback"><div className="panel-header"><h2>Feedback</h2>{!!issues.length && <span className="tag">{issues.length} finding{issues.length === 1 ? '' : 's'}</span>}</div>
    <FeedbackTabs id={tabsId} tab={tab} onSelect={onTab} />
    <div id={`${tabsId}-panel`} role="tabpanel" aria-labelledby={`${tabsId}-${tab}`} tabIndex={0} className="feedback-content" ref={listRef}>
    <CheckFilters exam={exam} category={category} tab={tab} onSelect={onFilter} />
    {result && <p className="helper feedback-status" role="status">{label ? `${label} feedback · ` : ''}{tab === 'text' ? 'Select a correction to highlight its wording in the exam.' : 'Structure, overall feedback and assessments. Linked paragraphs are highlighted in the exam.'}</p>}
    {!result ? <div className="empty-state"><h3>{exam.status === 'failed' ? 'Try checking again' : 'Criteria saved'}</h3><p>Feedback will appear here after checking. No findings have been generated for this photo.</p></div> : !issues.length ? (tab === 'text' || !hasAssessments) && <div className="empty-state"><h3>{tab === 'text' ? 'No text corrections' : 'No essay findings'}</h3><p>The original wording is preserved in the transcription.</p></div> : <div className="feedback-list">{issues.map(issue => <div key={issue.id} data-check={issue.category}>
      <button type="button" className={`feedback-card ${selectedId === issue.id ? 'selected' : ''}`} aria-pressed={selectedId === issue.id} onClick={() => onSelect(issue.id)}><span className="eyebrow">{categoryLabel(issue)}{issue.scope !== 'span' ? ' · Suggestion' : ''}</span>{issue.quote && <span className="issue-quote">{issue.quote}</span>}<strong>{issue.suggestion}</strong></button>
      {selectedId === issue.id && <div className="feedback-detail" aria-live="polite"><h3>{issue.scope === 'span' ? 'Why this change?' : 'A suggestion to consider'}</h3><p>{issue.explanation}</p><FeedbackLocation exam={exam} issue={issue} onLocate={onLocate} /></div>}
    </div>)}</div>}
    {tab === 'essay' && <>{!!result?.warnings?.length && <div className="notice"><h3>Review notes</h3><ul>{result.warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></div>}<AssessmentFeedback exam={exam} /></>}
    </div>
  </section>;
}

function AssessmentFeedback({ exam }: { exam: Exam }) {
  const result = exam.result;
  const assessment = result?.assessment ?? exam.criteria.assessment;
  const assessmentTitle = assessment === 'cae' ? 'Previous C1 assessment' : assessment === 'b2_first_schools' ? 'B2 assessment' : 'Writing assessment';
  return <>
    {!!result?.assessments?.length && <section className="assessment-feedback" aria-label={assessmentTitle}><h3>{assessmentTitle}</h3><p className="helper">Estimated bands for teacher review, not an official Cambridge score.</p><dl>{result.assessments.map(item => <div key={item.criterion}><dt>{item.criterion}<span className="tag">{item.score === null ? 'Not assessable' : `${item.score} / 5`}</span></dt><dd>{item.feedback}</dd></div>)}</dl></section>}
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
  const previewUnavailable = demo && (criteria.assessment === 'b2_first_schools' || !!criteria.customCriteria.trim() || criteria.checks.some(check => !['grammar', 'spelling', 'punctuation', 'structure'].includes(check)));
  if (criteria.assessment !== 'b2_first_schools' && !criteria.customCriteria.trim() && !previewUnavailable) return null;
  return <section className="panel criterion-summary" aria-label="Selected assessment criteria"><div className="panel-header"><h2>{criteria.assessment === 'b2_first_schools' ? 'Cambridge B2 First for Schools criteria' : 'Assessment criteria'}</h2></div><div className="panel-body">
    {criteria.assessment === 'b2_first_schools' && <p>Content · Communicative Achievement · Organisation · Language</p>}
    {criteria.customCriteria.trim() && <><h3>Custom criteria</h3><ul>{criteria.customCriteria.split('\n').filter(line => line.trim()).map((line, index) => <li key={index}>{line}</li>)}</ul></>}
    {previewUnavailable && <p className="helper">These criteria are saved. This preview does not generate an assessment or score for them.</p>}
  </div></section>;
}

export function ReviewScreen({ exam, exams, capabilities, onOpen, onEdit, onBack }: ReviewScreenProps) {
  const [tab, setTab] = useState<FeedbackTab>(() => initialFeedbackTab(exam));
  const [selectedId, setSelectedId] = useState(() => tabIssues(exam, initialFeedbackTab(exam))[0]?.id || '');
  const [category, setCategory] = useState<CheckFilter>('all');
  const anchorPrefix = useId();
  const hasResult = !!exam.result;
  useEffect(() => { setTab(initialFeedbackTab(exam)); setCategory('all'); setSelectedId(tabIssues(exam, initialFeedbackTab(exam))[0]?.id || ''); }, [exam.id, hasResult]);
  useEffect(() => {
    const issues = filteredIssues(exam, category, tab);
    setSelectedId(value => issues.some(issue => issue.id === value) ? value : issues[0]?.id || '');
  }, [exam.result, category, tab]);
  function selectCategory(value: CheckFilter) {
    setCategory(value);
    setSelectedId(filteredIssues(exam, value, tab)[0]?.id || '');
  }
  function selectTab(value: FeedbackTab) {
    setTab(value); setCategory('all'); setSelectedId(tabIssues(exam, value)[0]?.id || '');
  }
  function showParagraph(paragraphId: string) {
    const paragraph = document.getElementById(`${anchorPrefix}-${paragraphId}`);
    paragraph?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    paragraph?.focus({ preventScroll: true });
  }
  const wordCount = exam.result ? exam.result.wordCount ?? countWords(exam.result.paragraphs) : null;
  const task = exam.result?.task ?? { text: exam.criteria.taskText, source: exam.criteria.taskSource };
  const label = exam.studentName ? `${exam.studentName} — ${exam.title}` : exam.title;
  const status = isChecking(exam.status) ? 'Checking' : exam.status === 'failed' ? 'Check failed' : exam.status === 'completed' ? (capabilities.demo && exam.source === 'upload' ? 'Demo checked' : 'Checked') : 'Not checked';
  return <>
    <div className="screen-heading"><div><p className="eyebrow">EXAM REVIEW</p><h1>{label}</h1><p className="muted">{status} · {exam.source === 'sample' ? 'Sample exam' : `${exam.photos.length} photo${exam.photos.length === 1 ? '' : 's'}`}</p></div><div className="heading-actions"><button className="button" onClick={onBack}>Back to exam list</button><button className="button" disabled={isChecking(exam.status)} onClick={onEdit}>Edit task &amp; criteria</button></div></div>
    <ExamNavigation exam={exam} exams={exams} onOpen={onOpen} />
    <div className="review-layout">
      <Transcript key={`${exam.id}:${tab}:${category}`} exam={exam} issues={filteredIssues(exam, category, tab)} selectedId={selectedId} onSelect={setSelectedId} demo={capabilities.demo} anchorPrefix={anchorPrefix} />
      <Feedback exam={exam} category={category} tab={tab} selectedId={selectedId} onSelect={setSelectedId} onFilter={selectCategory} onTab={selectTab} onLocate={showParagraph} />
    </div>
    <aside className="review-sidebar" aria-label="Original exam and task details">
    <PhotoViewer key={exam.id} photos={exam.photos} exam={exam} compact />
    <section className="panel review-task" aria-label="Task instructions used"><div className="panel-header"><h2>Task instructions</h2><span className="muted">{task.source === 'essay' ? 'From essay page' : task.source === 'photo' ? 'From task photo' : task.text ? 'Reviewed / typed instructions' : 'Not supplied'}</span></div><p className="task-text">{task.text || 'No task supplied. Task completion cannot be assessed without instructions.'}</p></section>
    <section className="panel word-summary" aria-label="Word count"><div><strong className="metric">{wordCount === null ? 'Word count pending' : `${wordCount} words`}</strong><p className="helper">Student answer only · Title and task instructions excluded{exam.result?.needsTranscriptionReview && exam.result.wordCount !== undefined ? ' · Approximate while handwriting is uncertain' : ''}</p></div><p>{wordCount === null ? 'Available after transcription' : resultWordLimit(exam, wordCount)}</p></section>
    <CriteriaSummary exam={exam} demo={capabilities.demo} />
    </aside>
  </>;
}
