import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { defaultCriteria } from '../criteria';
import type { Capabilities, Exam, Issue, Photo } from '../types';
import * as media from '../data/media';
import { CriteriaScreen } from './CriteriaScreen';
import { ReviewScreen } from './ReviewScreen';
import { countWords, segmentParagraph } from './transcript';

const demo: Capabilities = { advancedCriteria: true, delete: true, taskPhoto: true, demo: true };
const api: Capabilities = { advancedCriteria: false, delete: false, taskPhoto: false, demo: false };
function exam(overrides: Partial<Exam> = {}): Exam {
  return { id: 'a', folderId: 'folder', title: 'Essay', studentName: 'Anna', criteria: defaultCriteria(), photos: [], source: 'sample', status: 'uploaded', ...overrides };
}

describe('CriteriaScreen', () => {
  it('uses editable B2 defaults, keeps teacher limits, and saves the selected exam identities', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<CriteriaScreen exams={[exam()]} capabilities={demo} busy={false} onSave={onSave} onCancel={vi.fn()} />);
    expect(screen.getByRole('option', { name: 'Cambridge B2 First for Schools' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /C1|CAE/ })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'b2_first_schools');
    expect(screen.getByLabelText('Minimum words')).toHaveValue('140');
    expect(screen.getByLabelText('Maximum words')).toHaveValue('190');
    await user.clear(screen.getByLabelText('Maximum words'));
    await user.type(screen.getByLabelText('Maximum words'), '300');
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'general');
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'b2_first_schools');
    expect(screen.getByLabelText('Maximum words')).toHaveValue('300');
    await user.click(screen.getByRole('button', { name: 'Check exam' }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ assessment: 'b2_first_schools', minWords: '140', maxWords: '300' }), true, { title: 'Essay', studentName: 'Anna' });
  });

  it('allows B2 assessment without individual correction checks', async () => {
    const user = userEvent.setup();
    render(<CriteriaScreen exams={[exam({ criteria: { ...defaultCriteria(), checks: [] } })]} capabilities={demo} busy={false} onSave={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Check exam' })).toBeDisabled();
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'b2_first_schools');
    await user.clear(screen.getByLabelText('Minimum words'));
    await user.clear(screen.getByLabelText('Maximum words'));
    expect(screen.getByRole('button', { name: 'Check exam' })).toBeEnabled();
  });

  it('keeps bulk task blank and draft values stable across polling object changes', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    const first = exam({ criteria: { ...defaultCriteria(), taskText: 'Task A' } });
    const second = exam({ id: 'b', criteria: { ...defaultCriteria(), taskText: 'Task B' } });
    const { rerender } = render(<CriteriaScreen exams={[first, second]} capabilities={demo} busy={false} onSave={onSave} onCancel={vi.fn()} />);
    expect(screen.getByLabelText('Shared task transcription')).toHaveValue('');
    await user.type(screen.getByLabelText(/Custom criteria/), 'Use examples');
    rerender(<CriteriaScreen exams={[structuredClone(first), structuredClone(second)]} capabilities={demo} busy={false} onSave={onSave} onCancel={vi.fn()} />);
    expect(screen.getByLabelText(/Custom criteria/)).toHaveValue('Use examples');
    await user.click(screen.getByRole('button', { name: 'Check 2 selected exams' }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ taskText: '', taskPhoto: undefined, customCriteria: 'Use examples' }), true, undefined);
  });

  it('rejects invalid word ranges and allows saving without selected checks', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<CriteriaScreen exams={[exam({ criteria: { ...defaultCriteria(), checks: [] } })]} capabilities={demo} busy={false} onSave={onSave} onCancel={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Check exam' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save exam' })).toBeEnabled();
    await user.type(screen.getByLabelText('Minimum words'), '300');
    await user.type(screen.getByLabelText('Maximum words'), '200');
    expect(screen.getByRole('alert')).toHaveTextContent('Maximum words must be at least the minimum.');
    expect(screen.getByRole('button', { name: 'Save exam' })).toBeDisabled();
    expect(onSave).not.toHaveBeenCalled();
  });

  it('disables unsupported server criteria without blocking basic checks', () => {
    render(<CriteriaScreen exams={[exam()]} capabilities={api} busy={false} onSave={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole('checkbox', { name: /Grammar/ })).toBeEnabled();
    expect(screen.getByRole('checkbox', { name: /^Format/ })).toBeDisabled();
    expect(screen.getByLabelText(/Custom criteria/)).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Take task photo' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Check exam' })).toBeEnabled();
  });

  it('waits for task photos before saving and does not invent a transcription', async () => {
    const user = userEvent.setup();
    let finish!: (photo: Photo) => void;
    const pending = new Promise<Photo>(resolve => { finish = resolve; });
    const preparation = vi.spyOn(media, 'preparePhoto').mockReturnValueOnce(pending);
    const { container } = render(<CriteriaScreen exams={[exam()]} capabilities={demo} busy={false} onSave={vi.fn()} onCancel={vi.fn()} />);
    await user.upload(container.querySelector<HTMLInputElement>('input[type="file"]')!, new File(['photo'], 'task.png', { type: 'image/png' }));
    expect(screen.getByRole('button', { name: 'Check exam' })).toBeDisabled();
    await act(async () => {
      finish({ id: 'task', url: 'data:image/png;base64,AA==', fileName: 'task.png', pageNumber: 1 });
      await pending;
    });
    expect(screen.getByRole('img', { name: 'Task instructions photo' })).toBeInTheDocument();
    expect(screen.getByLabelText('Task transcription')).toHaveValue('');
    expect(screen.getByRole('button', { name: 'Check exam' })).toBeEnabled();
    preparation.mockRestore();
  });

  it('preserves original task photos for the API instead of using demo previews', async () => {
    const user = userEvent.setup();
    const original = { id: 'task', url: 'data:image/png;base64,iVBORw==', fileName: 'task.png', pageNumber: 1 };
    const preparation = vi.spyOn(media, 'prepareTaskPhoto').mockResolvedValueOnce(original);
    const preview = vi.spyOn(media, 'preparePhoto');
    try {
      const { container } = render(<CriteriaScreen exams={[exam()]} capabilities={{ ...api, taskPhoto: true }} busy={false} onSave={vi.fn()} onCancel={vi.fn()} />);
      const file = new File(['original photo'], 'task.png', { type: 'image/png' });
      await user.upload(container.querySelector<HTMLInputElement>('input[type="file"]')!, file);
      expect(preparation).toHaveBeenCalledWith(file);
      expect(preview).not.toHaveBeenCalled();
      expect(screen.getByRole('img', { name: 'Task instructions photo' })).toHaveAttribute('src', original.url);
    } finally { preparation.mockRestore(); preview.mockRestore(); }
  });
});

const issue: Issue = { id: 'i1', category: 'grammar', scope: 'span', paragraphId: 'p', quote: 'practice', start: 10, end: 18, suggestion: 'practices', explanation: 'Use singular subject agreement.' };
describe('ReviewScreen and transcript ranges', () => {
  it('separates text corrections from essay feedback and filters each tab', async () => {
    const user = userEvent.setup();
    const spelling: Issue = { id: 'spelling', category: 'spelling', scope: 'span', paragraphId: 'p', quote: 'daly', start: 19, end: 23, suggestion: 'daily', explanation: 'Correct the spelling of daily.' };
    const structure: Issue = { id: 'structure', category: 'structure', scope: 'paragraph', paragraphId: 'p', suggestion: 'Add a supporting example.', explanation: 'Support the main idea with a concrete example.' };
    const value = exam({ status: 'completed', criteria: { ...defaultCriteria(), assessment: 'b2_first_schools' }, result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'My sister practice daly.' }], issues: [issue, spelling, structure], assessments: [{ criterion: 'Language', score: 3, feedback: 'Meaning remains clear.' }] } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={demo} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    const feedback = screen.getByRole('region', { name: 'Review feedback' });
    const transcript = () => screen.getByRole('region', { name: 'Exam transcription' });
    const filters = () => screen.getByRole('group', { name: 'Filter feedback by check' });
    expect(screen.getByRole('tab', { name: /Text corrections/ })).toHaveAttribute('aria-selected', 'true');
    expect(within(filters()).getByRole('button', { name: 'All feedback 2' })).toBeInTheDocument();
    expect(within(feedback).queryByText(structure.suggestion)).not.toBeInTheDocument();
    expect(within(filters()).queryByRole('button', { name: /Essay structure/ })).not.toBeInTheDocument();

    await user.click(within(filters()).getByRole('button', { name: 'Spelling 1' }));
    expect(within(feedback).getByText(spelling.explanation)).toBeInTheDocument();
    expect(within(feedback).queryByText(issue.suggestion)).not.toBeInTheDocument();
    expect(within(transcript()).getByRole('button', { name: 'Spelling feedback for daly' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(transcript()).queryByRole('button', { name: 'Grammar feedback for practice' })).not.toBeInTheDocument();
    expect(transcript().querySelector('.essay p')).toHaveTextContent('My sister practice daly.');

    await user.click(within(filters()).getByRole('button', { name: 'Grammar 1' }));
    expect(within(feedback).getByText(issue.explanation)).toBeInTheDocument();
    expect(within(transcript()).getByRole('button', { name: 'Grammar feedback for practice' })).toHaveAttribute('aria-pressed', 'true');

    await user.click(screen.getByRole('tab', { name: /Essay feedback/ }));
    expect(within(filters()).getByRole('button', { name: 'All feedback 1' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(filters()).queryByRole('button', { name: /Grammar/ })).not.toBeInTheDocument();
    await user.click(within(filters()).getByRole('button', { name: 'Essay structure 1' }));
    expect(within(feedback).getByText(structure.explanation)).toBeInTheDocument();
    expect(transcript().querySelector('.paragraph-focus p')?.textContent).toBe('My sister practice daly.');

    await user.click(screen.getByRole('tab', { name: 'B2 assessment' }));
    expect(screen.getByRole('tabpanel', { name: 'B2 assessment' })).toHaveTextContent('Meaning remains clear.');
    expect(screen.queryByRole('group', { name: 'Filter feedback by check' })).not.toBeInTheDocument();
    expect(within(feedback).queryByText(structure.suggestion)).not.toBeInTheDocument();
    expect(within(feedback).queryByText(issue.suggestion)).not.toBeInTheDocument();
    expect(transcript().querySelector('.paragraph-focus')).toBeNull();
    expect(within(transcript()).queryByRole('button', { name: /feedback for/ })).not.toBeInTheDocument();
    expect(transcript().querySelector('.essay p')?.textContent).toBe('My sister practice daly.');

    await user.click(screen.getByRole('tab', { name: /Text corrections/ }));
    expect(within(filters()).getByRole('button', { name: 'All feedback 2' })).toHaveAttribute('aria-pressed', 'true');
    expect(transcript().querySelector('.paragraph-focus')).toBeNull();
    await user.click(within(transcript()).getByRole('button', { name: 'Spelling feedback for daly' }));
    expect(within(feedback).getByRole('button', { name: 'Spelling daly daily' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(feedback).getByText(spelling.explanation)).toBeInTheDocument();
  });

  it('resets the feedback tab and category when navigating to another exam', async () => {
    const user = userEvent.setup();
    const structure: Issue = { id: 'structure', category: 'structure', scope: 'paragraph', paragraphId: 'p', suggestion: 'Add an example.', explanation: 'Support the main idea.' };
    const value = exam({ status: 'completed', result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'My sister practice daily.' }], issues: [issue, structure] } });
    const props = { exams: [value], capabilities: demo, onOpen: vi.fn(), onEdit: vi.fn(), onBack: vi.fn() };
    const { rerender } = render(<ReviewScreen {...props} exam={value} />);
    await user.click(screen.getByRole('tab', { name: /Essay feedback/ }));
    await user.click(screen.getByRole('button', { name: 'Essay structure 1' }));
    rerender(<ReviewScreen {...props} exam={{ ...value, id: 'next' }} />);
    expect(screen.getByRole('button', { name: 'All feedback 1' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('tab', { name: /Text corrections/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('region', { name: 'Exam transcription' }).querySelector('.paragraph-focus')).toBeNull();
    expect(screen.getByText(issue.explanation)).toBeInTheDocument();
  });

  it('navigates feedback tabs with the keyboard', async () => {
    const user = userEvent.setup();
    const value = exam({ status: 'completed', criteria: { ...defaultCriteria(), assessment: 'b2_first_schools' }, result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'My sister practice daily.' }], issues: [issue] } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={demo} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    await user.click(screen.getByRole('tab', { name: /Text corrections/ }));
    await user.keyboard('{ArrowRight}');
    const essayTab = screen.getByRole('tab', { name: /Essay feedback/ });
    expect(essayTab).toHaveFocus();
    expect(essayTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel', { name: /Essay feedback/ })).toBeInTheDocument();
    await user.keyboard('{ArrowRight}');
    const assessmentTab = screen.getByRole('tab', { name: 'B2 assessment' });
    expect(assessmentTab).toHaveFocus();
    expect(assessmentTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel', { name: 'B2 assessment' })).toHaveTextContent('No B2 assessment');
    await user.keyboard('{ArrowRight}');
    const textTab = screen.getByRole('tab', { name: /Text corrections/ });
    expect(textTab).toHaveFocus();
    expect(textTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel', { name: /Text corrections/ })).toHaveTextContent(issue.explanation);
    await user.keyboard('{ArrowLeft}');
    expect(assessmentTab).toHaveFocus();
    await user.keyboard('{Home}');
    expect(textTab).toHaveFocus();
    await user.keyboard('{End}');
    expect(assessmentTab).toHaveFocus();
    expect(assessmentTab).toHaveAttribute('tabindex', '0');
    expect(textTab).toHaveAttribute('tabindex', '-1');
  });

  it('opens newly completed essay feedback without resetting the chosen tab on later polling', async () => {
    const user = userEvent.setup();
    const pending = exam({ status: 'processing' });
    const props = { exams: [pending], capabilities: demo, onOpen: vi.fn(), onEdit: vi.fn(), onBack: vi.fn() };
    const { rerender } = render(<ReviewScreen {...props} exam={pending} />);
    const structure: Issue = { id: 'structure', category: 'structure', scope: 'paragraph', paragraphId: 'p', suggestion: 'Develop this example.', explanation: 'Explain how the example supports the argument.' };
    const completed = { ...pending, status: 'completed' as const, result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'An example.' }], issues: [structure] } };

    rerender(<ReviewScreen {...props} exam={completed} />);
    expect(screen.getByRole('tab', { name: /Essay feedback/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel', { name: /Essay feedback/ })).toHaveTextContent(structure.explanation);

    await user.click(screen.getByRole('tab', { name: /Text corrections/ }));
    rerender(<ReviewScreen {...props} exam={structuredClone(completed)} />);
    expect(screen.getByRole('tab', { name: /Text corrections/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByRole('tabpanel', { name: /Essay feedback/ })).not.toBeInTheDocument();
  });

  it('locates paragraph feedback without inventing a location for whole-essay feedback', async () => {
    const user = userEvent.setup();
    const text = 'A second paragraph with  original spacing.\nAnother line.';
    const paragraph: Issue = { id: 'paragraph', category: 'structure', scope: 'paragraph', paragraphId: 'p2', suggestion: 'Explain this example.', explanation: 'Connect the evidence to the main idea.' };
    const document: Issue = { id: 'document', category: 'structure', scope: 'document', suggestion: 'Add a conclusion.', explanation: 'Bring the main arguments together.' };
    const value = exam({ status: 'completed', result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'An introduction.' }, { id: 'p2', pageNumber: 2, text }], issues: [document, paragraph] } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={demo} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    const feedback = screen.getByRole('region', { name: 'Review feedback' });
    const transcript = screen.getByRole('region', { name: 'Exam transcription' });
    expect(screen.getByRole('tab', { name: /Essay feedback/ })).toHaveAttribute('aria-selected', 'true');
    expect(within(feedback).getByText('Whole essay')).toBeInTheDocument();
    expect(transcript.querySelector('.paragraph-focus')).toBeNull();
    expect(within(feedback).queryByRole('button', { name: /Show paragraph/ })).not.toBeInTheDocument();

    await user.click(within(feedback).getByRole('button', { name: /Explain this example/ }));
    const focused = transcript.querySelector<HTMLElement>('.paragraph-focus')!;
    expect(focused.querySelector('p')?.textContent).toBe(text);
    expect(focused).toHaveTextContent(/Page 2/);
    expect(focused).toHaveTextContent(/Paragraph 2/);
    focused.scrollIntoView = vi.fn();
    await user.click(within(feedback).getByRole('button', { name: 'Show paragraph 2' }));
    expect(focused.scrollIntoView).toHaveBeenCalled();

    await user.click(within(feedback).getByRole('button', { name: /Add a conclusion/ }));
    expect(transcript.querySelector('.paragraph-focus')).toBeNull();
    expect(within(feedback).getByText('Whole essay')).toBeInTheDocument();
  });

  it('preserves paragraph text and ignores malformed spans while respecting UTF-16 offsets', () => {
    const paragraph = { id: 'p', pageNumber: 1, text: '😀 sister practice daily.' };
    const start = paragraph.text.indexOf('practice');
    const valid = { ...issue, start, end: start + 'practice'.length };
    const parts = segmentParagraph(paragraph, [valid, { ...issue, id: 'bad', start: -1 }, { ...valid, id: 'quote-mismatch', quote: 'wrong' }]);
    expect(parts.map(part => part.text).join('')).toBe(paragraph.text);
    expect(parts.filter(part => part.issueIds.length).map(part => part.text)).toEqual(['practice']);
    expect(parts.flatMap(part => part.issueIds)).toEqual(['i1']);
  });

  it('renders uploaded pending photos without sample findings', () => {
    const uploaded = exam({ source: 'upload', status: 'completed', photos: [{ id: 'photo', url: 'data:image/jpeg;base64,AA==', fileName: 'anna.jpg', pageNumber: 1 }] });
    render(<ReviewScreen exam={uploaded} exams={[uploaded]} capabilities={demo} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByRole('img', { name: /Exam photo: Anna/ })).toHaveAttribute('src', 'data:image/jpeg;base64,AA==');
    expect(screen.getByText('Transcription pending')).toBeInTheDocument();
    expect(screen.getByText('Word count pending')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Grammar Pending' })).toBeDisabled();
    expect(screen.queryByText('My sister practice')).not.toBeInTheDocument();
  });

  it('shows B2 scores in their own tab and keeps custom criteria and review notes in essay feedback', async () => {
    const user = userEvent.setup();
    const value = exam({ status: 'completed', source: 'upload', criteria: { ...defaultCriteria(), assessment: 'b2_first_schools' }, result: {
      paragraphs: [{ id: 'p', pageNumber: 1, text: 'Student text with [unreadable] words.' }], issues: [], needsTranscriptionReview: true,
      task: { text: 'Discuss the benefits of learning languages.', source: 'photo' }, wordCount: 5,
      wordLimit: { minimum: 140, maximum: 190, status: 'below' },
      assessments: [{ criterion: 'Content', feedback: 'The unclear passage prevents a reliable band.', score: null }, { criterion: 'Organisation', feedback: 'Ideas follow a logical order.', score: 3 }],
      customAssessments: [{ criterion: 'Use examples', feedback: 'Add a concrete example to support the argument.' }],
      warnings: ['Check the unclear passage against the photo.'],
    } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={{ ...api, advancedCriteria: true }} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByRole('region', { name: 'Task instructions used' })).toHaveTextContent('Discuss the benefits of learning languages.');
    expect(screen.getByText('From task photo')).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Word count' })).toHaveTextContent('5 words');
    expect(screen.getByText('Below the 140-word minimum')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /Essay feedback/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('region', { name: 'Custom criteria assessment' })).toHaveTextContent('Add a concrete example');
    expect(screen.getByText('Check the unclear passage against the photo.')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'B2 assessment' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'B2 assessment' }));
    const assessment = screen.getByRole('region', { name: 'B2 assessment' });
    expect(assessment).toHaveTextContent('not an official Cambridge score');
    expect(assessment).toHaveTextContent('Not assessable');
    expect(assessment).toHaveTextContent('3 / 5');
    expect(screen.queryByRole('region', { name: 'Custom criteria assessment' })).not.toBeInTheDocument();
    expect(screen.queryByText('Check the unclear passage against the photo.')).not.toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: /Text corrections/ }));
    expect(screen.queryByRole('region', { name: 'B2 assessment' })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Custom criteria assessment' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: /Essay feedback/ }));
    expect(screen.getByRole('region', { name: 'Custom criteria assessment' })).toHaveTextContent('Add a concrete example');
    expect(screen.queryByRole('region', { name: 'B2 assessment' })).not.toBeInTheDocument();
  });

  it('opens completed B2-only results without resetting the chosen tab on later polling', async () => {
    const user = userEvent.setup();
    const pending = exam({ status: 'processing', criteria: { ...defaultCriteria(), assessment: 'b2_first_schools' } });
    const props = { exams: [pending], capabilities: api, onOpen: vi.fn(), onEdit: vi.fn(), onBack: vi.fn() };
    const { rerender } = render(<ReviewScreen {...props} exam={pending} />);
    await user.click(screen.getByRole('tab', { name: 'B2 assessment' }));
    expect(screen.getByRole('tabpanel', { name: 'B2 assessment' })).toHaveTextContent('Assessment pending');
    expect(screen.queryByRole('group', { name: 'Filter feedback by check' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'Text corrections' }));

    const completed = exam({ ...pending, status: 'completed', result: {
      assessment: 'b2_first_schools', paragraphs: [{ id: 'p', pageNumber: 1, text: 'A clear response.' }], issues: [],
      assessments: [{ criterion: 'Content', feedback: 'All relevant points are addressed.', score: 5 }],
    } });
    rerender(<ReviewScreen {...props} exam={completed} />);
    expect(screen.getByRole('tab', { name: 'B2 assessment' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel', { name: 'B2 assessment' })).toHaveTextContent('All relevant points are addressed.');
    expect(screen.getByRole('region', { name: 'B2 assessment' })).toHaveTextContent('5 / 5');
    expect(screen.queryByText('No essay findings')).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'Text corrections' }));
    rerender(<ReviewScreen {...props} exam={structuredClone(completed)} />);
    expect(screen.getByRole('tab', { name: 'Text corrections' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByRole('region', { name: 'B2 assessment' })).not.toBeInTheDocument();
  });

  it('labels saved C1 scores as previous results instead of relabeling them B2', () => {
    const value = exam({ status: 'completed', criteria: { ...defaultCriteria(), assessment: 'b2_first_schools' }, result: {
      assessment: 'cae', paragraphs: [{ id: 'p', pageNumber: 1, text: 'Saved response.' }], issues: [],
      assessments: [{ criterion: 'Language', feedback: 'Saved C1 evidence.', score: 3 }],
    } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={api} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByRole('tab', { name: 'Previous C1 assessment' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('region', { name: 'Previous C1 assessment' })).toHaveTextContent('3 / 5');
    expect(screen.queryByRole('region', { name: 'B2 assessment' })).not.toBeInTheDocument();
  });

  it('shows safe text, uncertainty, feedback selection, and counts only response text', async () => {
    const user = userEvent.setup();
    const text = 'My sister practice daily. <img src=x onerror=alert(1)>';
    const value = exam({ status: 'completed', criteria: { ...defaultCriteria(), taskText: 'Task '.repeat(100) }, result: {
      paragraphs: [{ id: 'p', pageNumber: 1, text, uncertainRanges: [{ start: 19, end: 24, certainty: 'uncertain' }] }], issues: [issue], needsTranscriptionReview: true,
    } });
    const { container } = render(<ReviewScreen exam={value} exams={[value]} capabilities={demo} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    const transcript = screen.getByRole('region', { name: 'Exam transcription' });
    expect(transcript.querySelector('.essay p')?.textContent).toBe(text);
    expect(transcript.querySelector('img')).toBeNull();
    expect(transcript.querySelector('.uncertainty')).toHaveTextContent('daily');
    await user.click(within(transcript).getByRole('button', { name: 'Grammar feedback for practice' }));
    expect(within(screen.getByRole('tooltip')).getByText('Use singular subject agreement.')).toBeInTheDocument();
    expect(transcript.querySelector('.essay p')?.textContent).toBe(text);
    expect(countWords(value.result!.paragraphs)).toBe(7);
    expect(container.querySelector('.word-summary')).toHaveTextContent('7 words');
  });
});
