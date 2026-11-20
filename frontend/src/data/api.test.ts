import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defaultCriteria } from '../criteria';
import type { Check, Criteria, ExamStatus } from '../types';
import { createApiRepository, normalizeApiResult } from './api';

const result = {
  paragraphs: [{ id: 'p1', page_number: 1, text: '🙂 She go home.', uncertain_ranges: [{ start: 3, end: 6, certainty: 'uncertain' }] }],
  issues: [{ id: 'i1', category: 'grammar' as const, scope: 'span' as const, paragraph_id: 'p1', quote: 'go', start: 7, end: 9, suggestion: 'goes', explanation: 'Use the singular verb.' }],
  needs_transcription_review: true, validation_warnings: ['Check one uncertain word.'],
  task: { text: 'Discuss daily routines.', source: 'essay' as const }, word_count: 3,
  word_limit: { minimum: 220, maximum: 260, status: 'below' as const },
  assessments: [{ criterion: 'Content', feedback: 'Not enough readable evidence.', score: null }],
  custom_assessments: [{ criterion: 'Use examples', feedback: 'Add a relevant example.' }],
};
function apiCriteria(criteria: Criteria = defaultCriteria()) {
  return { checks: criteria.checks, assessment: criteria.assessment, custom_criteria: criteria.customCriteria,
    min_words: criteria.minWords ? Number(criteria.minWords) : null, max_words: criteria.maxWords ? Number(criteria.maxWords) : null,
    detect_task: criteria.detectTask, task_text: criteria.taskText, task_source: criteria.taskSource, task_photo: criteria.taskPhoto || '' };
}
interface ServerExam {
  id: string; folder_id: string; title: string; student_name: string; checks: Check[];
  criteria: ReturnType<typeof apiCriteria>; batch_id: string; progress: number; stage: string;
  status: ExamStatus; pages: { id: string; page_number: number; url: string }[];
  result: typeof result | null; error: string | null;
}
interface CheckBody { request_id: string; exam_ids?: string[]; criteria: ReturnType<typeof apiCriteria>; criteria_by_exam?: Record<string, ReturnType<typeof apiCriteria>> }
const response = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const normalizedTaskBase64 = '/9j/2wCEAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsKCwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRQBAwQEBQQFCQUFCRQNCw0UFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFP/AABEIAAEAAQMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/AP1TooooA//Z';
const normalizedTaskPhoto = `data:image/jpeg;base64,${normalizedTaskBase64}`;
const originalTaskPhoto = `data:image/jpeg;base64,${btoa(atob(normalizedTaskBase64) + 'original metadata'.repeat(256))}`;

function blobDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

function server(count = 3) {
  const exams: ServerExam[] = Array.from({ length: count }, (_, index) => ({
    id: `e${index + 1}`, folder_id: 'f1', title: `Essay ${index + 1}`, student_name: `Student ${index + 1}`,
    checks: ['grammar', 'spelling'], criteria: apiCriteria({ ...defaultCriteria(), checks: ['grammar', 'spelling'] }),
    status: 'uploaded', result: null, error: null, batch_id: '', progress: 0, stage: 'uploaded',
    pages: [{ id: `page-${index + 1}`, page_number: 1, url: `/api/exams/e${index + 1}/pages/1` }],
  }));
  const control = { authenticated: true, checkStatus: 202, interruptOnce: false, taskPhotoStatus: 200 };
  const accepted = new Map<string, { batch_id: string; exam_ids: string[] }>();
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/auth/me' || url.pathname === '/auth/login') return control.authenticated ? response({ username: 'teacher', display_name: 'Teacher' }) : response({ detail: 'Invalid credentials' }, 401);
    if (url.pathname === '/auth/logout') return new Response(null, { status: 204 });
    if (!control.authenticated) return response({ detail: 'Sign in required' }, 401);
    if (init.method === 'DELETE') return new Response(null, { status: 204 });
    if (url.pathname === '/api/folders') return response([{ id: 'f1', name: 'Class', exam_count: exams.length }]);
    if (url.pathname === '/api/folders/f1/exams') {
      if (init.method === 'POST') return response({}, 201);
      const offset = Number(url.searchParams.get('offset'));
      return response(exams.slice(offset, offset + 100).map(({ pages: _pages, result: _result, error: _error, ...exam }) => exam));
    }
    if (url.pathname.endsWith('/task-photos')) return response({ url: url.pathname + '/task-id' }, 201);
    if (url.pathname.endsWith('/task-photos/task-id')) {
      if (control.taskPhotoStatus !== 200) return response({ detail: 'Task photo unavailable' }, control.taskPhotoStatus);
      const bytes = Uint8Array.from(atob(normalizedTaskBase64), character => character.charCodeAt(0));
      return new Response(bytes, { headers: { 'Content-Type': 'image/jpeg' } });
    }
    if (url.pathname.endsWith('/checks')) {
      const body = JSON.parse(String(init.body)) as CheckBody;
      if (accepted.has(body.request_id)) return response(accepted.get(body.request_id), 202);
      if (control.checkStatus !== 202) return response({ detail: 'Checking is unavailable' }, control.checkStatus);
      const ids = body.exam_ids || [url.pathname.split('/')[3]];
      const batch = { batch_id: `batch-${accepted.size + 1}`, exam_ids: ids };
      for (const exam of exams.filter(item => ids.includes(item.id))) {
        const criteria = body.criteria_by_exam?.[exam.id] ?? body.criteria;
        Object.assign(exam, { status: 'queued', criteria, checks: criteria.checks, result: null, batch_id: batch.batch_id, progress: 0, stage: 'queued' });
      }
      accepted.set(body.request_id, batch);
      if (control.interruptOnce) { control.interruptOnce = false; throw new TypeError('Connection lost after acceptance'); }
      return response(batch, 202);
    }
    const exam = exams.find(item => url.pathname === `/api/exams/${item.id}`);
    if (!exam) return response({ detail: 'Not found' }, 404);
    if (init.method === 'PATCH') Object.assign(exam, JSON.parse(String(init.body)));
    return response(exam);
  });
  const posts = () => fetchMock.mock.calls.filter(([url, init]) => String(url).endsWith('/checks') && init?.method === 'POST');
  return { exams, control, accepted, fetch: fetchMock, posts };
}

beforeEach(() => localStorage.clear());

describe('Go API repository', () => {
  it('reuses an accepted check request ID after its response times out', async () => {
    const backend = server(1);
    let interrupt = true;
    const send: typeof fetch = async (url, init) => {
      const result = await backend.fetch(url, init);
      if (String(url).endsWith('/checks') && interrupt) {
        interrupt = false;
        return new Promise<Response>(() => {});
      }
      return result;
    };
    const repository = createApiRepository({ fetch: send, requestTimeoutMs: 30 });
    await expect(repository.checkExams(['e1'], defaultCriteria())).rejects.toThrow('too long');
    await repository.checkExams(['e1'], defaultCriteria());

    expect(backend.accepted.size).toBe(1);
    expect(backend.posts()).toHaveLength(2);
    expect(backend.posts()[0][1]?.body).toEqual(backend.posts()[1][1]?.body);
  });

  it('maps real criteria, server progress, task, assessments, and paragraph-local UTF-16 offsets', async () => {
    const backend = server(1);
    Object.assign(backend.exams[0], { status: 'completed', result, progress: 100, stage: 'completed', batch_id: 'batch-1', criteria: apiCriteria({ ...defaultCriteria(), assessment: 'cae', minWords: '220', maxWords: '260' }) });
    const exam = (await createApiRepository({ fetch: backend.fetch }).load()).exams[0];
    expect(exam.criteria).toMatchObject({ assessment: 'cae', minWords: '220', detectTask: true });
    expect(exam).toMatchObject({ progress: 100, stage: 'completed', batchId: 'batch-1' });
    expect(exam.photos[0].url).toBe('/api/exams/e1/pages/1');
    const issue = exam.result!.issues[0];
    expect(exam.result!.paragraphs[0].text.slice(issue.start!, issue.end!)).toBe('go');
    expect(exam.result).toEqual(normalizeApiResult(result));
    expect(exam.result).toMatchObject({ task: result.task, wordCount: 3, assessments: result.assessments, customAssessments: result.custom_assessments });
    expect(backend.fetch.mock.calls.every(([, init]) => init?.credentials === 'include')).toBe(true);
  });

  it('loads every page instead of omitting exams beyond the page limit', async () => {
    const backend = server(101);
    expect((await createApiRepository({ fetch: backend.fetch }).load()).exams).toHaveLength(101);
    expect(backend.fetch.mock.calls.some(([url]) => String(url) === '/api/folders/f1/exams?limit=100&offset=100')).toBe(true);
  });

  it('keeps explicitly refreshed exam details in the next workspace load', async () => {
    const backend = server(1);
    Object.assign(backend.exams[0], { status: 'completed', result });
    const repository = createApiRepository({ fetch: backend.fetch });
    await repository.load();
    backend.exams[0].result = { ...result, validation_warnings: ['Latest review warning.'] };
    await repository.getExam('e1');
    expect((await repository.load()).exams[0].result?.warnings).toEqual(['Latest review warning.']);
    expect(backend.fetch.mock.calls.filter(([url]) => url === '/api/exams/e1')).toHaveLength(2);
  });

  it('submits the entire selected batch atomically and only reads jobs after reload', async () => {
    const backend = server();
    const criteria = { ...defaultCriteria(), checks: ['spelling', 'taskCompletion'] as Check[], customCriteria: 'Use examples' };
    await createApiRepository({ fetch: backend.fetch }).checkExams(['e1', 'e2', 'e1'], criteria);
    expect(backend.posts()).toHaveLength(1);
    expect(backend.posts()[0][0]).toBe('/api/folders/f1/checks');
    expect(JSON.parse(String(backend.posts()[0][1]?.body))).toMatchObject({ exam_ids: ['e1', 'e2'], criteria: { checks: criteria.checks, custom_criteria: 'Use examples' } });
    const reopened = await createApiRepository({ fetch: backend.fetch }).load();
    expect(reopened.exams.map(exam => exam.status)).toEqual(['queued', 'queued', 'uploaded']);
    expect(reopened.exams[0].batchId).toBe(reopened.exams[1].batchId);
    expect(backend.posts()).toHaveLength(1);
    expect(backend.fetch.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(false);
  });

  it('does not clear prior feedback by PATCH before a rejected recheck', async () => {
    const backend = server(1);
    Object.assign(backend.exams[0], { status: 'completed', result });
    backend.control.checkStatus = 503;
    const repository = createApiRepository({ fetch: backend.fetch });
    await expect(repository.checkExams(['e1'], defaultCriteria())).rejects.toThrow('Checking is unavailable');
    expect(backend.exams[0].result).toEqual(result);
    expect(backend.fetch.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(false);
    expect((await repository.getExam('e1')).status).toBe('completed');
  });

  it('retries an ambiguous submission using the same request ID without queueing duplicate work', async () => {
    const backend = server(1);
    backend.control.interruptOnce = true;
    const repository = createApiRepository({ fetch: backend.fetch });
    await expect(repository.checkExams(['e1'], defaultCriteria())).rejects.toThrow('server could not be reached');
    expect(backend.exams[0].status).toBe('queued');
    await repository.checkExams(['e1'], defaultCriteria());
    const bodies = backend.posts().map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[0].request_id).toBe(bodies[1].request_id);
    expect(backend.accepted.size).toBe(1);
  });

  it('retires an ambiguous request after polling confirms its batch so an intentional recheck starts new work', async () => {
    const backend = server(2);
    backend.control.interruptOnce = true;
    const repository = createApiRepository({ fetch: backend.fetch });
    await expect(repository.checkExams(['e1', 'e2'], defaultCriteria())).rejects.toThrow('server could not be reached');
    backend.exams.forEach(exam => Object.assign(exam, { status: 'completed', result, progress: 100, stage: 'completed' }));
    await repository.load();
    await repository.checkExams(['e1', 'e2'], defaultCriteria());
    const bodies = backend.posts().map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[0].request_id).not.toBe(bodies[1].request_id);
    expect(backend.accepted.size).toBe(2);
  });

  it('waits for every exam to confirm a new shared batch before retiring a retry request', async () => {
    const backend = server(2);
    backend.control.interruptOnce = true;
    const repository = createApiRepository({ fetch: backend.fetch });
    await expect(repository.checkExams(['e1', 'e2'], defaultCriteria())).rejects.toThrow('server could not be reached');
    await repository.getExam('e1');
    await repository.checkExams(['e1', 'e2'], defaultCriteria());
    const bodies = backend.posts().map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[0].request_id).toBe(bodies[1].request_id);
    expect(backend.accepted.size).toBe(1);
  });

  it('uploads a shared original once and reuses its server-normalized bytes for each remaining exam', async () => {
    const backend = server(3);
    const repository = createApiRepository({ fetch: backend.fetch });
    await repository.checkExams(['e1', 'e2', 'e3'], { ...defaultCriteria(), taskPhoto: originalTaskPhoto });
    const uploads = backend.fetch.mock.calls.filter(([url]) => String(url).endsWith('/task-photos'));
    expect(uploads.map(([url]) => url)).toEqual(['/api/exams/e1/task-photos', '/api/exams/e2/task-photos', '/api/exams/e3/task-photos']);
    const files = uploads.map(([, init]) => (init?.body as FormData).get('photo') as File);
    expect(await Promise.all(files.map(blobDataUrl))).toEqual([originalTaskPhoto, normalizedTaskPhoto, normalizedTaskPhoto]);
    expect(files[1].size).toBeLessThan(files[0].size);
    const downloads = backend.fetch.mock.calls.filter(([url]) => String(url).endsWith('/task-photos/task-id'));
    expect(downloads.map(([url]) => url)).toEqual(['/api/exams/e1/task-photos/task-id']);
    expect(downloads[0][1]).toMatchObject({ credentials: 'include', cache: 'no-store' });
    const body = JSON.parse(String(backend.posts()[0][1]?.body));
    expect(body.criteria_by_exam.e1.task_photo).toBe('/api/exams/e1/task-photos/task-id');
    expect(body.criteria_by_exam.e2.task_photo).toBe('/api/exams/e2/task-photos/task-id');
    expect(body.criteria_by_exam.e3.task_photo).toBe('/api/exams/e3/task-photos/task-id');
    expect(JSON.stringify(body)).not.toContain('data:image');
  });

  it('uploads the original again when the cached normalized source was deleted', async () => {
    const backend = server(3);
    const repository = createApiRepository({ fetch: backend.fetch });
    const criteria = { ...defaultCriteria(), taskPhoto: originalTaskPhoto };
    await repository.saveExam('e1', { criteria });
    backend.control.taskPhotoStatus = 404;
    await repository.saveExam('e2', { criteria });
    backend.control.taskPhotoStatus = 200;
    await repository.saveExam('e3', { criteria });
    const uploads = backend.fetch.mock.calls.filter(([url]) => String(url).endsWith('/task-photos'));
    const files = uploads.map(([, init]) => (init?.body as FormData).get('photo') as File);
    expect(await Promise.all(files.map(blobDataUrl))).toEqual([originalTaskPhoto, originalTaskPhoto, normalizedTaskPhoto]);
    const downloads = backend.fetch.mock.calls.filter(([url]) => String(url).endsWith('/task-photos/task-id'));
    expect(downloads.map(([url]) => url)).toEqual(['/api/exams/e1/task-photos/task-id', '/api/exams/e2/task-photos/task-id']);
  });

  it.each([401, 503])('propagates normalized-source failures with status %s without uploading again', async status => {
    const backend = server(2);
    const repository = createApiRepository({ fetch: backend.fetch });
    const criteria = { ...defaultCriteria(), taskPhoto: originalTaskPhoto };
    await repository.saveExam('e1', { criteria });
    backend.control.taskPhotoStatus = status;
    await expect(repository.saveExam('e2', { criteria })).rejects.toMatchObject({ status, message: 'Task photo unavailable' });
    expect(backend.fetch.mock.calls.filter(([url]) => String(url).endsWith('/task-photos'))).toHaveLength(1);
  });

  it('sends advanced fields for saving and supported deletion requests', async () => {
    const backend = server(1);
    const repository = createApiRepository({ fetch: backend.fetch });
    expect(repository.capabilities).toEqual({ advancedCriteria: true, delete: true, taskPhoto: true, demo: false });
    await repository.saveExam('e1', { title: ' New title ', criteria: { ...defaultCriteria(), assessment: 'cae', minWords: '220', maxWords: '260', taskText: 'Discuss education.', taskSource: 'manual' } });
    const saved = backend.fetch.mock.calls.find(([, init]) => init?.method === 'PATCH')!;
    expect(JSON.parse(String(saved[1]?.body))).toMatchObject({ title: 'New title', criteria: { assessment: 'cae', min_words: 220, max_words: 260, task_text: 'Discuss education.' } });
    await repository.deleteExam('e1');
    await repository.deleteFolder('f1');
    expect(backend.fetch.mock.calls.filter(([, init]) => init?.method === 'DELETE').map(([url]) => url)).toEqual(['/api/exams/e1', '/api/folders/f1']);
  });

  it('uses the existing multipart upload field names and avoids browser-stored credentials', async () => {
    const backend = server(1);
    const repository = createApiRepository({ fetch: backend.fetch });
    await repository.login('teacher', 'test-only-password');
    await repository.upload('f1', new File(['image'], 'Anna.png', { type: 'image/png' }));
    const upload = backend.fetch.mock.calls.find(([url, init]) => url === '/api/folders/f1/exams' && init?.method === 'POST')!;
    const form = upload[1]!.body as FormData;
    expect(form.get('title')).toBe('Anna');
    expect(form.getAll('checks')).toEqual(['grammar', 'spelling', 'punctuation', 'structure']);
    expect(form.get('photos')).toBeInstanceOf(File);
    expect(localStorage.length).toBe(0);
    await repository.logout();
  });

  it('returns a signed-out session on 401 and propagates invalid limits before sending requests', async () => {
    const backend = server(1);
    backend.control.authenticated = false;
    const repository = createApiRepository({ fetch: backend.fetch });
    expect(await repository.session()).toBeNull();
    backend.fetch.mockClear();
    await expect(repository.checkExams(['e1'], { ...defaultCriteria(), minWords: '300', maxWords: '200' })).rejects.toThrow('Maximum words');
    expect(backend.fetch).not.toHaveBeenCalled();
  });
});
