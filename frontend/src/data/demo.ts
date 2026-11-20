import { criteriaError, defaultCriteria, isChecking } from '../criteria';
import type { Criteria, Exam, ExamChanges, Repository, Workspace } from '../types';
import { preparePhoto } from './media';
import { migrateLegacy, normalizeCriteria, normalizeWorkspace } from './normalize';
import { samplePhoto, sampleResult, sampleTask } from './sample';

export const DEMO_STORAGE_KEY = 'examcheck:react:workspace:v1';
const LEGACY_STORAGE_PREFIX = 'codex:visualization-widget-state-v2:';
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value));

function saveWorkspace(storage: Storage, workspace: Workspace): void {
  const serialized = JSON.stringify(workspace);
  if (serialized.length > 2200000) throw new Error('Browser preview storage is full. Remove some exams or use smaller photos.');
  try { storage.setItem(DEMO_STORAGE_KEY, serialized); }
  catch { throw new Error('Changes could not be saved. Browser storage is unavailable or full.'); }
}

function readWorkspace(storage: Storage): Workspace {
  const current = storage.getItem(DEMO_STORAGE_KEY);
  if (current !== null) return normalizeWorkspace(JSON.parse(current));
  const key = `${LEGACY_STORAGE_PREFIX}${JSON.stringify([location.pathname, location.search])}`;
  const legacy = storage.getItem(key) ?? storage.getItem(`${LEGACY_STORAGE_PREFIX}["/",""]`);
  let workspace: Workspace = { folders: [], exams: [] };
  if (legacy) {
    try { workspace = migrateLegacy(JSON.parse(legacy)); }
    catch { throw new Error('The previous preview data could not be read. It has not been overwritten.'); }
  }
  saveWorkspace(storage, workspace);
  return workspace;
}

function validName(value: string, allowEmpty = false): string {
  const name = value.trim();
  if ((!allowEmpty && !name) || name.length > 120) throw new Error('Use a name of 1–120 characters.');
  return name;
}

function findExam(workspace: Workspace, id: string): Exam {
  const exam = workspace.exams.find(item => item.id === id);
  if (!exam) throw new Error('This exam no longer exists.');
  return exam;
}

function checkFolder(workspace: Workspace, id: string): void {
  if (!workspace.folders.some(folder => folder.id === id)) throw new Error('This folder no longer exists.');
}

function distinctFolder(workspace: Workspace, name: string, exceptId?: string): void {
  if (workspace.folders.some(folder => folder.id !== exceptId && folder.name.toLocaleLowerCase() === name.toLocaleLowerCase())) throw new Error('A folder with this name already exists.');
}

function appliedCriteria(exam: Exam, criteria: Criteria, bulk: boolean): Criteria {
  const result = clone(criteria);
  if (bulk && !result.taskText.trim() && !result.taskPhoto) {
    result.taskText = exam.criteria.taskText;
    result.taskSource = exam.criteria.taskSource;
    result.taskPhoto = exam.criteria.taskPhoto;
  }
  if (!result.taskText.trim() && !result.taskPhoto && result.detectTask && exam.source === 'sample') {
    result.taskText = sampleTask;
    result.taskSource = 'essay';
  }
  return result;
}

function advanceChecks(workspace: Workspace): boolean {
  let changed = false;
  for (const exam of workspace.exams) {
    if (!isChecking(exam.status) || !exam.demoStartedAt) continue;
    const elapsed = Date.now() - exam.demoStartedAt;
    const status = elapsed < 0 ? 'queued' : elapsed < 3200 ? 'processing' : 'completed';
    if (exam.status !== status) changed = true;
    exam.status = status;
    exam.progress = Math.min(100, Math.max(0, Math.floor(elapsed / 32)));
    if (status === 'completed') exam.result = exam.source === 'sample' ? sampleResult(exam.criteria.checks) : undefined;
  }
  return changed;
}

export function createDemoRepository(storage: Storage = localStorage): Repository {
  const mutate = (change: (workspace: Workspace) => void) => {
    const workspace = clone(readWorkspace(storage));
    change(workspace);
    saveWorkspace(storage, workspace);
  };
  return {
    capabilities: { advancedCriteria: true, delete: true, taskPhoto: true, demo: true },
    session: async () => ({ username: 'demo', displayName: 'Demo workspace' }),
    login: async () => ({ username: 'demo', displayName: 'Demo workspace' }),
    logout: async () => {},
    async load() {
      const workspace = readWorkspace(storage);
      if (advanceChecks(workspace)) saveWorkspace(storage, workspace);
      workspace.folders.forEach(folder => { folder.examCount = workspace.exams.filter(exam => exam.folderId === folder.id).length; });
      return clone(workspace);
    },
    async getExam(id) { return findExam(await this.load(), id); },
    async createFolder(value) {
      const name = validName(value);
      mutate(workspace => { distinctFolder(workspace, name); workspace.folders.push({ id: crypto.randomUUID(), name }); });
    },
    async renameFolder(id, value) {
      const name = validName(value);
      mutate(workspace => { checkFolder(workspace, id); distinctFolder(workspace, name, id); workspace.folders.find(folder => folder.id === id)!.name = name; });
    },
    async deleteFolder(id) {
      mutate(workspace => { checkFolder(workspace, id); workspace.folders = workspace.folders.filter(folder => folder.id !== id); workspace.exams = workspace.exams.filter(exam => exam.folderId !== id); });
    },
    async upload(folderId, file) {
      checkFolder(readWorkspace(storage), folderId);
      const photo = await preparePhoto(file);
      mutate(workspace => {
        checkFolder(workspace, folderId);
        workspace.exams.push({ id: crypto.randomUUID(), folderId, title: file.name.replace(/\.[^.]+$/, '').slice(0, 120) || 'Exam', studentName: '', status: 'uploaded', criteria: defaultCriteria(), photos: [photo], source: 'upload' });
      });
    },
    async addSamples(folderId) {
      mutate(workspace => {
        checkFolder(workspace, folderId);
        for (const studentName of ['Anna', 'Ben', 'Chloe']) workspace.exams.push({ id: crypto.randomUUID(), folderId, title: 'Language essay', studentName, status: 'uploaded', criteria: { ...defaultCriteria(), taskText: sampleTask, taskSource: 'essay' }, photos: [samplePhoto(studentName)], source: 'sample' });
      });
    },
    async saveExam(id: string, changes: ExamChanges) {
      mutate(workspace => {
        const exam = findExam(workspace, id);
        if (changes.criteria && isChecking(exam.status)) throw new Error('Wait for the current check to finish before changing criteria.');
        if (changes.title !== undefined) exam.title = validName(changes.title);
        if (changes.studentName !== undefined) exam.studentName = validName(changes.studentName, true);
        if (changes.criteria) Object.assign(exam, { criteria: normalizeCriteria(changes.criteria), status: 'uploaded', result: undefined, error: undefined, progress: undefined, demoStartedAt: undefined });
      });
    },
    async deleteExam(id) { mutate(workspace => { findExam(workspace, id); workspace.exams = workspace.exams.filter(exam => exam.id !== id); }); },
    async checkExams(ids, criteria) {
      const error = criteriaError(criteria);
      if (error) throw new Error(error);
      mutate(workspace => {
        const exams = [...new Set(ids)].map(id => findExam(workspace, id));
        if (!exams.length) throw new Error('Select an exam to check.');
        if (new Set(exams.map(exam => exam.folderId)).size > 1) throw new Error('Select exams from one folder.');
        if (exams.some(exam => isChecking(exam.status))) throw new Error('Some selected exams are already being checked.');
        const batchId = crypto.randomUUID();
        exams.forEach((exam, index) => Object.assign(exam, { criteria: appliedCriteria(exam, criteria, exams.length > 1), status: 'queued', progress: 0, demoStartedAt: Date.now() + Math.min(index, 10) * 650, batchId, result: undefined, error: undefined }));
      });
    },
  };
}
