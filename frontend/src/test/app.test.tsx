import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import { defaultCriteria } from '../criteria';
import type { Exam, Repository, Workspace } from '../types';
import { ApiError, createApiRepository } from '../data/api';

function makeExam(id: string, status: Exam['status'] = 'uploaded'): Exam {
  return {
    id, folderId: 'folder-1', title: `Essay ${id}`, studentName: '', status,
    criteria: defaultCriteria(), photos: [], source: 'sample',
    ...(status === 'completed' ? { result: { paragraphs: [{ id: 'p1', text: 'A completed answer.', pageNumber: 1 }], issues: [] } } : {}),
  };
}

function makeRepository(initial: Workspace, authenticated = true) {
  const workspace = structuredClone(initial);
  const user = { username: 'teacher', displayName: 'Teacher' };
  let session = authenticated ? user : null;
  const repository = {
    capabilities: { demo: authenticated, advancedCriteria: authenticated, delete: authenticated, taskPhoto: authenticated },
    session: vi.fn(async () => session),
    login: vi.fn(async (_username: string, _password: string) => { session = user; return user; }),
    logout: vi.fn(async () => { session = null; }),
    load: vi.fn(async () => structuredClone(workspace)),
    getExam: vi.fn(async (id: string) => structuredClone(workspace.exams.find(exam => exam.id === id)!)),
    createFolder: vi.fn(async (name: string) => { workspace.folders.push({ id: 'folder-1', name }); }),
    renameFolder: vi.fn(async () => {}),
    deleteFolder: vi.fn(async () => {}),
    upload: vi.fn(async (folderId: string, file: File) => {
      workspace.exams.push({ ...makeExam(`upload-${workspace.exams.length + 1}`), folderId, title: file.name, source: 'upload' });
    }),
    addSamples: vi.fn(async () => {}),
    saveExam: vi.fn<Repository['saveExam']>(async (id, changes) => { Object.assign(workspace.exams.find(exam => exam.id === id)!, changes); }),
    deleteExam: vi.fn(async () => {}),
    checkExams: vi.fn<Repository['checkExams']>(async (ids, criteria) => {
      for (const exam of workspace.exams.filter(item => ids.includes(item.id))) {
        Object.assign(exam, { criteria, status: 'processing', progress: 42 });
      }
    }),
  } satisfies Repository;
  return repository;
}

beforeEach(() => { window.history.replaceState(null, '', '/'); });

describe('React workspace workflows', () => {
  it('releases upload controls after a server failure and accepts a retry', async () => {
    window.history.replaceState(null, '', '/#/folders/folder-1');
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Class A' }], exams: [] });
    repository.upload.mockRejectedValueOnce(new ApiError('Photo storage is unavailable.', 500));
    render(<App repository={repository} />);
    await screen.findByRole('heading', { name: 'Class A' });
    const photo = new File(['photo'], 'Anna.jpg', { type: 'image/jpeg' });

    await user.upload(screen.getByLabelText('Upload exams', { selector: 'input' }), photo);
    expect(await screen.findByRole('alert')).toHaveTextContent('Photo storage is unavailable.');
    expect(screen.getByRole('button', { name: 'Upload exams' })).toBeEnabled();
    expect(screen.queryByText(/Adding photo/)).not.toBeInTheDocument();
    await user.upload(screen.getByLabelText('Upload exams', { selector: 'input' }), photo);
    expect(await screen.findByRole('checkbox', { name: 'Select exam: Anna.jpg' })).toBeEnabled();
    expect(repository.upload).toHaveBeenCalledTimes(2);
  });

  it('preserves editable criteria and enables retry after a rejected check', async () => {
    window.history.replaceState(null, '', '/#/folders/folder-1/exams/one/check');
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Class A' }], exams: [makeExam('one')] });
    repository.checkExams.mockRejectedValueOnce(new ApiError('Checking is temporarily unavailable.', 500));
    render(<App repository={repository} />);
    const submit = await screen.findByRole('button', { name: 'Check exam' });
    await user.type(screen.getByLabelText('Student name'), 'Anna');
    await user.click(submit);

    expect(await screen.findByText('Checking is temporarily unavailable.')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Check exam' })).toBeEnabled());
    expect(screen.getByLabelText('Student name')).toHaveValue('Anna');
    await user.click(screen.getByRole('button', { name: 'Check exam' }));
    expect(await screen.findByRole('heading', { name: 'Class A' })).toBeInTheDocument();
    expect(repository.checkExams).toHaveBeenCalledTimes(2);
  });

  it('allows a failed exam detail request to be retried in place', async () => {
    window.history.replaceState(null, '', '/#/folders/folder-1/exams/one/check');
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Class A' }], exams: [makeExam('one')] });
    repository.getExam.mockRejectedValueOnce(new Error('Exam details are temporarily unavailable.'));
    render(<App repository={repository} />);

    await user.click(await screen.findByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('button', { name: 'Check exam' })).toBeEnabled();
    expect(repository.getExam).toHaveBeenCalledTimes(2);
  });

  it('does not repeat a saved action when only its refresh fails', async () => {
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [], exams: [] });
    render(<App repository={repository} />);
    await user.click(await screen.findByRole('button', { name: 'Create folder' }));
    await user.type(screen.getByLabelText('Folder name'), 'Saved class');
    repository.load.mockRejectedValueOnce(new Error('Connection interrupted.'));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The view could not be updated.');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(repository.createFolder).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByRole('link', { name: /Saved class/ })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(repository.createFolder).toHaveBeenCalledTimes(1);
  });

  it.each(['request', 'response body'])('releases sign-in controls when the %s stalls', async mode => {
    const user = userEvent.setup();
    const send = vi.fn<typeof fetch>(async url => String(url) === '/auth/me'
      ? new Response(JSON.stringify({ detail: 'Sign in required' }), { status: 401 })
      : mode === 'request' ? new Promise<Response>(() => {}) : new Response(new ReadableStream()));
    render(<App repository={createApiRepository({ fetch: send, requestTimeoutMs: 10 })} />);
    await user.type(await screen.findByLabelText('Username'), 'teacher');
    await user.type(screen.getByLabelText('Password'), 'test-only-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('too long');
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled();
    expect(screen.getByLabelText('Password')).toHaveValue('');
  });

  it('keeps the workspace usable during a polling failure and clears its notice on recovery', async () => {
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Saved class' }], exams: [] });
    render(<App repository={repository} />);
    await screen.findByRole('link', { name: /Saved class/ });
    repository.load.mockRejectedValueOnce(new Error('Server unavailable.'));

    expect(await screen.findByRole('alert', {}, { timeout: 2500 })).toHaveTextContent('Server unavailable.');
    expect(screen.getByRole('button', { name: 'Create folder' })).toBeEnabled();
    expect(screen.getByRole('link', { name: /Saved class/ })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument(), { timeout: 2500 });
  });

  it('creates a named folder and opens its exam list', async () => {
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [], exams: [] });
    render(<App repository={repository} />);

    await user.click(await screen.findByRole('button', { name: 'Create folder' }));
    const dialog = screen.getByRole('dialog', { name: 'Create folder' });
    await user.type(within(dialog).getByLabelText('Folder name'), 'CAE November');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));

    const folder = await screen.findByRole('link', { name: /CAE November/ });
    expect(repository.createFolder).toHaveBeenCalledWith('CAE November');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(folder);
    expect(await screen.findByRole('heading', { name: 'CAE November' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Upload exams' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Take photo' })).toBeInTheDocument();
  });

  it('uploads into the list, then checks only the selected exams with per-exam progress', async () => {
    window.history.replaceState(null, '', '/#/folders/folder-1');
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Class A' }], exams: [makeExam('unselected')] });
    render(<App repository={repository} />);
    await screen.findByRole('heading', { name: 'Class A' });

    const photos = [new File(['photo-a'], 'Anna.jpg', { type: 'image/jpeg' }), new File(['photo-b'], 'Ben.png', { type: 'image/png' })];
    await user.upload(screen.getByLabelText('Upload exams', { selector: 'input' }), photos);
    const anna = await screen.findByRole('checkbox', { name: 'Select exam: Anna.jpg' });
    const ben = await screen.findByRole('checkbox', { name: 'Select exam: Ben.png' });
    expect(repository.upload).toHaveBeenCalledTimes(2);
    expect(repository.checkExams).not.toHaveBeenCalled();
    expect(screen.queryByRole('heading', { name: 'Check selected exams' })).not.toBeInTheDocument();

    await user.click(anna);
    expect(screen.getByRole('button', { name: 'Bulk check' })).toBeDisabled();
    await user.click(ben);
    await user.click(screen.getByRole('button', { name: 'Bulk check' }));
    expect(await screen.findByRole('heading', { name: 'Check selected exams' })).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Selected exams' })).queryByText('Essay unselected')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Check 2 selected exams' }));

    await screen.findByRole('heading', { name: 'Class A' });
    expect(repository.checkExams).toHaveBeenCalledWith(['upload-2', 'upload-3'], expect.objectContaining({ checks: ['grammar', 'spelling', 'punctuation', 'structure'] }));
    expect(screen.getByRole('progressbar', { name: 'Checking Anna.jpg' })).toHaveAttribute('value', '42');
    expect(screen.getByRole('progressbar', { name: 'Checking Ben.png' })).toHaveAttribute('value', '42');
    expect(screen.getByRole('checkbox', { name: 'Select exam: Anna.jpg' })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Select exam: Essay unselected' })).toBeEnabled();
  });

  it('opens checked exams in review and keeps processing exams unavailable', async () => {
    window.history.replaceState(null, '', '/#/folders/folder-1');
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Class A' }], exams: [makeExam('done', 'completed'), makeExam('working', 'processing')] });
    render(<App repository={repository} />);
    const checked = await screen.findByRole('button', { name: /Essay done Sample exam.*Checked/ });
    expect(screen.getByRole('checkbox', { name: 'Select exam: Essay working' })).toBeDisabled();
    expect(screen.getByRole('progressbar', { name: 'Checking Essay working' })).not.toHaveAttribute('value');

    await user.click(checked);
    expect(await screen.findByRole('heading', { name: 'Transcription' })).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Exam transcription' })).getByText('A completed answer.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '3 Review' })).toHaveAttribute('aria-current', 'step');
    expect(screen.getByRole('button', { name: 'Next exam' })).toBeDisabled();
  });

  it('submits sign-in credentials to the repository before loading the workspace', async () => {
    const user = userEvent.setup();
    const repository = makeRepository({ folders: [{ id: 'folder-1', name: 'Private exams' }], exams: [] }, false);
    render(<App repository={repository} />);
    await screen.findByRole('heading', { name: 'Sign in' });
    expect(repository.load).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText('Username'), 'teacher');
    await user.type(screen.getByLabelText('Password'), 'test-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByRole('link', { name: /Private exams/ })).toBeInTheDocument();
    expect(repository.login).toHaveBeenCalledWith('teacher', 'test-password');
    await user.click(screen.getByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Sign in' })).toBeInTheDocument());
    expect(screen.getByLabelText('Password')).toHaveValue('');
  });
});
