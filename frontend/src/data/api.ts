import { basicChecks, criteriaError, defaultCriteria, isChecking, normalizeAssessment } from '../criteria';
import type { Check, Criteria, Exam, ExamChanges, ExamResult, ExamStatus, Repository, Session, Workspace } from '../types';
import { maxPhotoSourceBytes, validatePhotoFile } from './media';

export class ApiError extends Error {
  constructor(message: string, readonly status: number) { super(message); this.name = 'ApiError'; }
}

interface ApiCriteria {
  checks: Check[]; assessment: Criteria['assessment'] | 'cae'; custom_criteria: string;
  min_words: number | null; max_words: number | null; detect_task: boolean;
  task_text: string; task_source: Criteria['taskSource']; task_photo: string;
}
interface ApiExam {
  id: string; folder_id: string; title: string; student_name: string; status: ExamStatus;
  checks: Check[]; criteria?: ApiCriteria; batch_id?: string; progress?: number; stage?: string;
  pages?: { id: string; page_number: number; url: string }[]; result?: ApiResult | null; error?: string | null;
}
interface ApiResult {
  assessment?: ExamResult['assessment'];
  paragraphs: { id: string; text: string; page_number: number; uncertain_ranges?: { start: number; end: number; certainty: string }[] }[];
  issues: { id: string; category: Check; scope: 'span' | 'paragraph' | 'document'; paragraph_id: string | null; quote?: string | null; start?: number | null; end?: number | null; suggestion: string; explanation: string }[];
  needs_transcription_review?: boolean; validation_warnings?: string[];
  task?: ExamResult['task']; word_count?: number; word_limit?: ExamResult['wordLimit'];
  assessments?: ExamResult['assessments']; custom_assessments?: ExamResult['customAssessments'];
}
interface ApiOptions { fetch?: typeof fetch; requestTimeoutMs?: number; uploadTimeoutMs?: number }
interface Submission {
  signature: string; path: string; body: Record<string, unknown>;
  examIds: string[]; previousBatchIds: Record<string, string | undefined>;
}
interface UploadedTask {
  urls: Map<string, string>;
  normalized?: Blob;
}

export function normalizeApiResult(result?: ApiResult | null): ExamResult | undefined {
  if (!result) return undefined;
  return {
    assessment: result.assessment,
    paragraphs: result.paragraphs.map(paragraph => ({ id: paragraph.id, text: paragraph.text, pageNumber: paragraph.page_number, uncertainRanges: paragraph.uncertain_ranges })),
    issues: result.issues.map(issue => ({ id: issue.id, category: issue.category, scope: issue.scope, paragraphId: issue.paragraph_id, quote: issue.quote, start: issue.start, end: issue.end, suggestion: issue.suggestion, explanation: issue.explanation })),
    needsTranscriptionReview: result.needs_transcription_review, warnings: result.validation_warnings,
    task: result.task, wordCount: result.word_count, wordLimit: result.word_limit,
    assessments: result.assessments, customAssessments: result.custom_assessments,
  };
}

function readCriteria(input: ApiExam): Criteria {
  const value = input.criteria;
  if (!value) return { ...defaultCriteria(), checks: input.checks, detectTask: false };
  return {
    checks: value.checks, customCriteria: value.custom_criteria,
    ...normalizeAssessment(value.assessment, value.min_words == null ? '' : String(value.min_words), value.max_words == null ? '' : String(value.max_words)),
    detectTask: value.detect_task, taskText: value.task_text, taskSource: value.task_source, taskPhoto: value.task_photo || undefined,
  };
}

function writeCriteria(value: Criteria): ApiCriteria {
  return {
    checks: value.checks, assessment: value.assessment, custom_criteria: value.customCriteria,
    min_words: value.minWords ? Number(value.minWords) : null, max_words: value.maxWords ? Number(value.maxWords) : null,
    detect_task: value.detectTask, task_text: value.taskText, task_source: value.taskSource, task_photo: value.taskPhoto || '',
  };
}

function mapExam(input: ApiExam): Exam {
  const result = normalizeApiResult(input.result);
  if (result && !result.assessment && input.criteria?.assessment === 'cae') result.assessment = 'cae';
  return {
    id: input.id, folderId: input.folder_id, title: input.title, studentName: input.student_name,
    status: input.status, criteria: readCriteria(input), source: 'upload', batchId: input.batch_id || undefined,
    progress: input.status === 'uploaded' ? undefined : input.progress, stage: input.stage,
    photos: (input.pages ?? []).map(page => ({ id: page.id, pageNumber: page.page_number, url: page.url, fileName: `Page ${page.page_number}` })),
    result, error: input.error ?? undefined,
  };
}

export function assertApiCriteria(criteria: Criteria, checking = false): void {
  const error = criteriaError(checking ? criteria : { ...criteria, checks: ['grammar'] });
  if (error) throw new Error(error);
}

function taskPhotoBlob(url: string): Blob {
  if (url.length > Math.ceil(maxPhotoSourceBytes / 3) * 4 + 32) throw new Error('Choose a valid task photo.');
  const match = /^data:(image\/(?:jpeg|png|webp));base64,([A-Za-z0-9+/=]+)$/.exec(url);
  if (!match) throw new Error('Choose a valid task photo.');
  const decoded = atob(match[2]);
  if (!decoded.length || decoded.length > maxPhotoSourceBytes) throw new Error('Choose a valid task photo.');
  const bytes = Uint8Array.from(decoded, character => character.charCodeAt(0));
  return new Blob([bytes], { type: match[1] });
}

export function createApiRepository(options: ApiOptions = {}): Repository {
  const send = options.fetch ?? globalThis.fetch.bind(globalThis);
  let username = '';
  let inFlight: Promise<Workspace> | undefined;
  let uncertainSubmission: Submission | undefined;
  const cache = new Map<string, { value: Exam; signature: string; at: number }>();
  const uploadedTasks = new Map<string, UploadedTask>();
  const json = (method: string, body: unknown): RequestInit => ({ method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

  async function readResponse<T>(url: string, init: RequestInit, read: (response: Response) => Promise<T>): Promise<T> {
    const controller = new AbortController();
    const timeout = init.body instanceof FormData ? options.uploadTimeoutMs ?? 120000 : options.requestTimeoutMs ?? 20000;
    let timer: ReturnType<typeof setTimeout>;
    const deadline = new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        reject(new ApiError('The server took too long to respond. Refresh to check the latest status before trying again.', 0));
        controller.abort();
      }, timeout);
    });
    const response = async () => {
      const result = await send(url, { ...init, signal: controller.signal, credentials: 'include', cache: 'no-store' });
      if (!result.ok) {
        const data = await result.json().catch(() => ({})) as { detail?: unknown };
        throw new ApiError(typeof data.detail === 'string' ? data.detail : `Request failed (${result.status}).`, result.status);
      }
      return read(result);
    };
    try { return await Promise.race([response(), deadline]); }
    catch (failure) {
      if (failure instanceof ApiError) throw failure;
      throw new ApiError('The server could not be reached. Check your connection and try again.', 0);
    } finally { clearTimeout(timer!); }
  }

  async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
    return readResponse(url, options, async response => {
      if (response.status === 204) return undefined as T;
      try { return await response.json() as T; }
      catch { throw new ApiError('The server response was interrupted. Refresh to check the latest status before trying again.', 0); }
    });
  }

  function clearSession(): void {
    username = ''; cache.clear(); uploadedTasks.clear(); uncertainSubmission = undefined;
  }
  function setSession(value: { username: string; display_name: string }): Session {
    if (username !== value.username) clearSession();
    username = value.username;
    return { username, displayName: value.display_name };
  }

  async function listAll<T>(url: string): Promise<T[]> {
    const rows: T[] = [];
    for (let offset = 0; ; offset += 100) {
      const page = await request<T[]>(`${url}?limit=100&offset=${offset}`);
      rows.push(...page);
      if (page.length < 100) return rows;
    }
  }

  const examSignature = (item: ApiExam) => JSON.stringify([item.status, item.title, item.student_name, item.criteria ?? item.checks, item.batch_id, item.progress, item.stage]);
  function cacheExam(item: ApiExam): Exam {
    const value = mapExam(item);
    cache.set(item.id, { value, signature: examSignature(item), at: Date.now() });
    return value;
  }
  function retireConfirmedSubmission(exams: Exam[]): void {
    if (!uncertainSubmission) return;
    const submission = uncertainSubmission;
    const selected = submission.examIds.map(id => exams.find(exam => exam.id === id));
    const batchId = selected[0]?.batchId;
    if (batchId && selected.every(exam => exam?.batchId === batchId && batchId !== submission.previousBatchIds[exam.id])) {
      uncertainSubmission = undefined;
    }
  }
  async function detail(item: ApiExam): Promise<Exam> {
    const stored = cache.get(item.id);
    if (stored?.signature === examSignature(item) && Date.now() - stored.at < 10000 && !isChecking(item.status)) return stored.value;
    return cacheExam(await request<ApiExam>(`/api/exams/${encodeURIComponent(item.id)}`));
  }
  async function fetchWorkspace(): Promise<Workspace> {
    const folders = await listAll<{ id: string; name: string; exam_count: number }>('/api/folders');
    const lists = await Promise.all(folders.map(folder => listAll<ApiExam>(`/api/folders/${encodeURIComponent(folder.id)}/exams`)));
    const exams: Exam[] = [];
    for (const list of lists) for (const exam of list) exams.push(await detail(exam));
    return { folders: folders.map(folder => ({ id: folder.id, name: folder.name, examCount: folder.exam_count })), exams };
  }
  async function load(): Promise<Workspace> {
    if (inFlight) return inFlight;
    inFlight = (async () => {
      if (!username) setSession(await request('/auth/me'));
      const workspace = await fetchWorkspace();
      retireConfirmedSubmission(workspace.exams);
      return workspace;
    })();
    try { return await inFlight; } finally { inFlight = undefined; }
  }

  async function taskUploadBlob(source: string, uploaded: UploadedTask): Promise<Blob> {
    if (uploaded.normalized) return uploaded.normalized;
    const previous = uploaded.urls.entries().next().value;
    if (!previous) return taskPhotoBlob(source);
    try {
      uploaded.normalized = await readResponse(previous[1], {}, async response => {
        try { return new Blob([await response.arrayBuffer()], { type: 'image/jpeg' }); }
        catch { throw new ApiError('The server response was interrupted. Retry to upload the task photo.', 0); }
      });
    }
    catch (error) {
      if (!(error instanceof ApiError) || error.status !== 404) throw error;
      uploaded.urls.delete(previous[0]);
      return taskPhotoBlob(source);
    }
    return uploaded.normalized;
  }

  async function prepareCriteria(id: string, criteria: Criteria): Promise<ApiCriteria> {
    const value = writeCriteria(criteria);
    if (!value.task_photo.startsWith('data:')) return value;
    let uploaded = uploadedTasks.get(value.task_photo);
    if (!uploaded) {
      uploaded = { urls: new Map() };
      uploadedTasks.set(value.task_photo, uploaded);
    }
    let url = uploaded.urls.get(id);
    if (!url) {
      const body = new FormData();
      body.append('photo', await taskUploadBlob(value.task_photo, uploaded), 'task-photo.jpg');
      const photo = await request<{ url: string }>(`/api/exams/${encodeURIComponent(id)}/task-photos`, { method: 'POST', body });
      url = photo.url;
      uploaded.urls.set(id, url);
    }
    return { ...value, task_photo: url };
  }

  async function prepareSubmission(ids: string[], criteria: Criteria, signature: string): Promise<Submission> {
    const workspace = await load();
    const exams = ids.map(id => workspace.exams.find(exam => exam.id === id));
    if (!ids.length || ids.length > 50 || exams.some(exam => !exam)) throw new Error('Select 1–50 existing exams to check.');
    if (exams.some(exam => isChecking(exam!.status))) throw new Error('Some selected exams are already being checked.');
    if (new Set(exams.map(exam => exam!.folderId)).size !== 1) throw new Error('Select exams from one folder.');
    const metadata = { signature, examIds: ids, previousBatchIds: Object.fromEntries(exams.map(exam => [exam!.id, exam!.batchId])) };
    const body: Record<string, unknown> = { request_id: crypto.randomUUID() };
    if (ids.length === 1) {
      body.criteria = await prepareCriteria(ids[0], criteria);
      return { ...metadata, path: `/api/exams/${encodeURIComponent(ids[0])}/checks`, body };
    }
    body.exam_ids = ids;
    body.criteria = writeCriteria({ ...criteria, taskPhoto: criteria.taskPhoto?.startsWith('data:') ? undefined : criteria.taskPhoto });
    if (criteria.taskPhoto?.startsWith('data:')) {
      const overrides: Record<string, ApiCriteria> = {};
      for (const id of ids) overrides[id] = await prepareCriteria(id, criteria);
      body.criteria_by_exam = overrides;
    }
    return { ...metadata, path: `/api/folders/${encodeURIComponent(exams[0]!.folderId)}/checks`, body };
  }

  async function checkExams(inputIds: string[], criteria: Criteria): Promise<void> {
    assertApiCriteria(criteria, true);
    const ids = [...new Set(inputIds)];
    const signature = JSON.stringify([[...ids].sort(), criteria]);
    const submission = uncertainSubmission?.signature === signature ? uncertainSubmission : await prepareSubmission(ids, criteria, signature);
    try {
      await request(submission.path, json('POST', submission.body));
      uncertainSubmission = undefined;
      ids.forEach(id => cache.delete(id));
    } catch (error) {
      uncertainSubmission = error instanceof ApiError && error.status === 0 ? submission : undefined;
      throw error;
    }
  }

  return {
    capabilities: { advancedCriteria: true, delete: true, taskPhoto: true, demo: false },
    async session() {
      try { return setSession(await request('/auth/me')); }
      catch (error) { if (error instanceof ApiError && error.status === 401) { clearSession(); return null; } throw error; }
    },
    async login(user, password) { return setSession(await request('/auth/login', json('POST', { username: user, password }))); },
    async logout() { await request('/auth/logout', { method: 'POST' }); clearSession(); },
    load,
    async getExam(id) {
      const exam = cacheExam(await request<ApiExam>(`/api/exams/${encodeURIComponent(id)}`));
      retireConfirmedSubmission([...cache.values()].map(item => item.value));
      return exam;
    },
    async createFolder(name) { await request('/api/folders', json('POST', { name: name.trim() })); },
    async renameFolder(id, name) { await request(`/api/folders/${encodeURIComponent(id)}`, json('PATCH', { name: name.trim() })); },
    async deleteFolder(id) { await request(`/api/folders/${encodeURIComponent(id)}`, { method: 'DELETE' }); cache.clear(); },
    async upload(folderId, file) {
      validatePhotoFile(file);
      const body = new FormData();
      body.append('title', file.name.replace(/\.[^.]+$/, '').slice(0, 120) || 'Exam');
      body.append('student_name', '');
      basicChecks.forEach(check => body.append('checks', check));
      body.append('photos', file);
      await request(`/api/folders/${encodeURIComponent(folderId)}/exams`, { method: 'POST', body });
    },
    async addSamples() { throw new Error('Sample exams are available in demo mode.'); },
    async saveExam(id: string, changes: ExamChanges) {
      const body: Record<string, unknown> = {};
      if (changes.title !== undefined) body.title = changes.title.trim();
      if (changes.studentName !== undefined) body.student_name = changes.studentName.trim();
      if (changes.criteria) { assertApiCriteria(changes.criteria); body.criteria = await prepareCriteria(id, changes.criteria); }
      await request(`/api/exams/${encodeURIComponent(id)}`, json('PATCH', body));
      cache.delete(id);
    },
    async deleteExam(id) { await request(`/api/exams/${encodeURIComponent(id)}`, { method: 'DELETE' }); cache.delete(id); },
    checkExams,
  };
}
