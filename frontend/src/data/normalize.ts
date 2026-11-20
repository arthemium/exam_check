import { checkOptions, defaultCriteria } from '../criteria';
import type { Check, Criteria, Exam, Workspace } from '../types';
import { samplePhoto, sampleResult } from './sample';

type RecordValue = Record<string, unknown>;
const record = (value: unknown): RecordValue => value && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
const text = (value: unknown, length = 120) => typeof value === 'string' ? value.slice(0, length) : '';
const validChecks = new Set(checkOptions.map(option => option.value));

export function normalizeCriteria(value: unknown): Criteria {
  const input = record(value);
  return {
    ...defaultCriteria(),
    checks: Array.isArray(input.checks) ? [...new Set(input.checks.filter((check): check is Check => validChecks.has(check as Check)))] : defaultCriteria().checks,
    assessment: input.assessment === 'cae' ? 'cae' : 'general',
    customCriteria: text(input.customCriteria, 1200), minWords: text(input.minWords, 5), maxWords: text(input.maxWords, 5),
    detectTask: input.detectTask !== false, taskText: text(input.taskText, 4000),
    taskSource: ['essay', 'photo', 'manual'].includes(String(input.taskSource)) ? input.taskSource as Criteria['taskSource'] : '',
    taskPhoto: typeof input.taskPhoto === 'string' && input.taskPhoto.startsWith('data:image/') ? input.taskPhoto : undefined,
  };
}

function legacyExam(value: unknown, folderIds: Set<string>): Exam | null {
  const item = record(value);
  if (!text(item.id) || !folderIds.has(text(item.folderId))) return null;
  const source = item.photoKind === 'upload' ? 'upload' : 'sample';
  const criteria = normalizeCriteria(item);
  const startedAt = typeof item.checkStartedAt === 'number' && Number.isFinite(item.checkStartedAt) ? item.checkStartedAt : undefined;
  const completed = item.reviewed === true;
  const photo = typeof item.image === 'string' && /^data:image\/jpeg;base64,[A-Za-z0-9+/=]+$/.test(item.image) ? item.image : '';
  return {
    id: text(item.id), folderId: text(item.folderId), title: text(item.title) || 'Exam', studentName: text(item.studentName), source,
    criteria, status: completed ? 'completed' : startedAt ? 'queued' : 'uploaded', demoStartedAt: startedAt,
    batchId: text(item.batchId), progress: completed ? 100 : undefined,
    photos: photo ? [{ id: crypto.randomUUID(), url: photo, fileName: text(item.fileName) || 'Exam photo', pageNumber: 1 }] : source === 'sample' ? [samplePhoto(text(item.studentName) || 'Student')] : [],
    result: completed && source === 'sample' ? sampleResult(criteria.checks) : undefined,
  };
}

export function migrateLegacy(value: unknown): Workspace {
  const outer = record(value);
  const data = record(outer.privateContent ?? outer);
  const folders = (Array.isArray(data.folders) ? data.folders : []).map(record).filter(folder => text(folder.id) && text(folder.name)).map(folder => ({ id: text(folder.id), name: text(folder.name) }));
  const ids = new Set(folders.map(folder => folder.id));
  const candidates = Array.isArray(data.exams) ? [...data.exams] : [];
  if (ids.has(text(data.bulkFolderId)) && Array.isArray(data.bulkDraft)) candidates.push(...data.bulkDraft.map(item => ({ ...record(item), folderId: data.bulkFolderId })));
  const exams = candidates.map(item => legacyExam(item, ids)).filter((exam): exam is Exam => !!exam);
  return { folders, exams: [...new Map(exams.map(exam => [exam.id, exam])).values()] };
}

export function normalizeWorkspace(value: unknown): Workspace {
  const input = record(value);
  if (!Array.isArray(input.folders) || !Array.isArray(input.exams)) throw new Error('Saved demo data is invalid. Export or clear this browser’s site storage to start again.');
  const folders = input.folders.map(record).filter(folder => text(folder.id) && text(folder.name)).map(folder => ({ id: text(folder.id), name: text(folder.name) }));
  const folderIds = new Set(folders.map(folder => folder.id));
  const exams = input.exams.filter(item => typeof item === 'object' && item !== null).map(item => item as Exam).filter(exam => typeof exam.id === 'string' && folderIds.has(exam.folderId)).map(exam => ({ ...exam, criteria: normalizeCriteria(exam.criteria) }));
  return { folders, exams };
}
