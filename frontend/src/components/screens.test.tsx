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
  it('uses editable CAE defaults, keeps teacher limits, and saves the selected exam identities', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<CriteriaScreen exams={[exam()]} capabilities={demo} busy={false} onSave={onSave} onCancel={vi.fn()} />);
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'cae');
    expect(screen.getByLabelText('Minimum words')).toHaveValue('220');
    expect(screen.getByLabelText('Maximum words')).toHaveValue('260');
    await user.clear(screen.getByLabelText('Maximum words'));
    await user.type(screen.getByLabelText('Maximum words'), '300');
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'general');
    await user.selectOptions(screen.getByLabelText('Assessment criteria'), 'cae');
    expect(screen.getByLabelText('Maximum words')).toHaveValue('300');
    await user.click(screen.getByRole('button', { name: 'Check exam' }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ assessment: 'cae', minWords: '220', maxWords: '300' }), true, { title: 'Essay', studentName: 'Anna' });
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
  it('filters feedback by requested check and highlights matching wording', async () => {
    const user = userEvent.setup();
    const spelling: Issue = { id: 'spelling', category: 'spelling', scope: 'span', paragraphId: 'p', quote: 'daly', start: 19, end: 23, suggestion: 'daily', explanation: 'Correct the spelling of daily.' };
    const structure: Issue = { id: 'structure', category: 'structure', scope: 'paragraph', paragraphId: 'p', suggestion: 'Add a supporting example.', explanation: 'Support the main idea with a concrete example.' };
    const value = exam({ status: 'completed', result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'My sister practice daly.' }], issues: [issue, spelling, structure] } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={demo} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    const feedback = screen.getByRole('region', { name: 'Review feedback' });
    const transcript = screen.getByRole('region', { name: 'Exam transcription' });
    const filters = screen.getByRole('group', { name: 'Filter feedback by check' });

    await user.click(within(filters).getByRole('button', { name: 'Spelling 1' }));
    expect(within(feedback).getByText(spelling.explanation)).toBeInTheDocument();
    expect(within(feedback).queryByText(issue.suggestion)).not.toBeInTheDocument();
    expect(within(transcript).getByRole('button', { name: 'Spelling feedback for daly' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(transcript).queryByRole('button', { name: 'Grammar feedback for practice' })).not.toBeInTheDocument();
    expect(transcript.querySelector('.essay p')).toHaveTextContent('My sister practice daly.');

    await user.click(within(filters).getByRole('button', { name: 'Grammar 1' }));
    expect(within(feedback).getByText(issue.explanation)).toBeInTheDocument();
    expect(within(transcript).getByRole('button', { name: 'Grammar feedback for practice' })).toHaveAttribute('aria-pressed', 'true');

    await user.click(within(filters).getByRole('button', { name: 'Punctuation 0' }));
    expect(within(feedback).getByText('No punctuation findings')).toBeInTheDocument();
    expect(within(transcript).queryByRole('button')).not.toBeInTheDocument();

    await user.click(within(filters).getByRole('button', { name: 'Essay structure 1' }));
    expect(within(feedback).getByText(structure.explanation)).toBeInTheDocument();
    expect(within(transcript).queryByRole('button')).not.toBeInTheDocument();

    await user.click(within(filters).getByRole('button', { name: 'All feedback 3' }));
    await user.click(within(transcript).getByRole('button', { name: 'Spelling feedback for daly' }));
    expect(within(feedback).getByRole('button', { name: 'Spelling daly daily' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(feedback).getByText(spelling.explanation)).toBeInTheDocument();
  });

  it('resets category selection when navigating to another exam', async () => {
    const user = userEvent.setup();
    const value = exam({ status: 'completed', result: { paragraphs: [{ id: 'p', pageNumber: 1, text: 'My sister practice daily.' }], issues: [issue] } });
    const props = { exams: [value], capabilities: demo, onOpen: vi.fn(), onEdit: vi.fn(), onBack: vi.fn() };
    const { rerender } = render(<ReviewScreen {...props} exam={value} />);
    await user.click(screen.getByRole('button', { name: 'Punctuation 0' }));
    rerender(<ReviewScreen {...props} exam={{ ...value, id: 'next' }} />);
    expect(screen.getByRole('button', { name: 'All feedback 1' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText(issue.explanation)).toBeInTheDocument();
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

  it('shows extracted task instructions, server word count, and qualified CAE/custom feedback', () => {
    const value = exam({ status: 'completed', source: 'upload', criteria: { ...defaultCriteria(), assessment: 'cae' }, result: {
      paragraphs: [{ id: 'p', pageNumber: 1, text: 'Student text with [unreadable] words.' }], issues: [], needsTranscriptionReview: true,
      task: { text: 'Discuss the benefits of learning languages.', source: 'photo' }, wordCount: 5,
      wordLimit: { minimum: 220, maximum: 260, status: 'below' },
      assessments: [{ criterion: 'Content', feedback: 'The unclear passage prevents a reliable band.', score: null }, { criterion: 'Organisation', feedback: 'Ideas follow a logical order.', score: 3 }],
      customAssessments: [{ criterion: 'Use examples', feedback: 'Add a concrete example to support the argument.' }],
    } });
    render(<ReviewScreen exam={value} exams={[value]} capabilities={{ ...api, advancedCriteria: true }} onOpen={vi.fn()} onEdit={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByRole('region', { name: 'Task instructions used' })).toHaveTextContent('Discuss the benefits of learning languages.');
    expect(screen.getByText('From task photo')).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Word count' })).toHaveTextContent('5 words');
    expect(screen.getByText('Below the 220-word minimum')).toBeInTheDocument();
    const assessment = screen.getByRole('region', { name: 'CAE assessment' });
    expect(assessment).toHaveTextContent('not an official Cambridge score');
    expect(assessment).toHaveTextContent('Not assessable');
    expect(assessment).toHaveTextContent('3 / 5');
    expect(screen.getByRole('region', { name: 'Custom criteria assessment' })).toHaveTextContent('Add a concrete example');
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
    expect(screen.getByText('Use singular subject agreement.')).toBeInTheDocument();
    expect(countWords(value.result!.paragraphs)).toBe(7);
    expect(container.querySelector('.word-summary')).toHaveTextContent('7 words');
  });
});
