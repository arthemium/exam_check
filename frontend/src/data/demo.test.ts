import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defaultCriteria } from '../criteria';
import { createDemoRepository, DEMO_STORAGE_KEY } from './demo';
import type { Workspace } from '../types';

const legacyKey = 'codex:visualization-widget-state-v2:["/",""]';
const jpeg = 'data:image/jpeg;base64,YWJj';

beforeEach(() => { localStorage.clear(); vi.useFakeTimers(); vi.setSystemTime(new Date('2026-11-20T12:00:00Z')); });
afterEach(() => vi.useRealTimers());

async function samples() {
  const repository = createDemoRepository();
  await repository.createFolder('CAE Class');
  const folder = (await repository.load()).folders[0];
  await repository.addSamples(folder.id);
  return { repository, folder, exams: (await repository.load()).exams };
}

describe('persistent demo workspace', () => {
  it('migrates names, photos, task criteria and checked flags once without resurrecting deleted data', async () => {
    localStorage.setItem(legacyKey, JSON.stringify({ privateContent: {
      folders: [{ id: 'f1', name: 'My class' }],
      exams: [{ id: 'e1', folderId: 'f1', title: 'Essay', studentName: 'Anna', photoKind: 'upload', image: jpeg,
        reviewed: true, checks: ['grammar', 'style'], taskText: 'Compare two viewpoints.', taskSource: 'manual', assessment: 'cae', minWords: '220', maxWords: '260', customCriteria: 'Explain your opinion.' }],
    } }));
    const repository = createDemoRepository();
    const exam = (await repository.load()).exams[0];
    expect(exam).toMatchObject({ title: 'Essay', studentName: 'Anna', status: 'completed', source: 'upload', photos: [{ url: jpeg }] });
    expect(exam.criteria).toMatchObject({ checks: ['grammar', 'style'], assessment: 'cae', taskText: 'Compare two viewpoints.', taskSource: 'manual', minWords: '220', maxWords: '260' });
    expect(exam.result).toBeUndefined();
    await repository.deleteFolder('f1');
    expect(await createDemoRepository().load()).toEqual({ folders: [], exams: [] });
    expect(localStorage.getItem(legacyKey)).not.toBeNull();
  });

  it('checks only selected exams, preserves individual tasks, and resumes progress after reload', async () => {
    const { repository, exams } = await samples();
    await repository.saveExam(exams[0].id, { criteria: { ...defaultCriteria(), taskText: 'Task A', taskSource: 'manual', taskPhoto: jpeg } });
    await repository.saveExam(exams[1].id, { criteria: { ...defaultCriteria(), taskText: 'Task B', taskSource: 'essay' } });
    await repository.checkExams([exams[0].id, exams[1].id, exams[0].id], { ...defaultCriteria(), checks: ['spelling'] });
    const running = await createDemoRepository().load();
    expect(running.exams.map(exam => exam.status)).toEqual(['processing', 'queued', 'uploaded']);
    expect(running.exams[0].criteria).toMatchObject({ taskText: 'Task A', taskPhoto: jpeg, checks: ['spelling'] });
    expect(running.exams[1].criteria.taskText).toBe('Task B');
    expect(running.exams[2].criteria.checks).toEqual(defaultCriteria().checks);
    vi.advanceTimersByTime(4000);
    const completed = await createDemoRepository().load();
    expect(completed.exams.map(exam => exam.status)).toEqual(['completed', 'completed', 'uploaded']);
    expect(completed.exams[0].progress).toBe(100);
    expect(completed.exams[0].result?.issues.map(issue => issue.category)).toEqual(['spelling']);
    expect(completed.exams[0].batchId).toBe(completed.exams[1].batchId);
  });

  it('does not invent a transcript or feedback for an uploaded exam', async () => {
    const { exams, folder } = await samples();
    const workspace: Workspace = { folders: [folder], exams: [{ ...exams[0], source: 'upload', photos: [{ id: 'photo', url: jpeg, fileName: 'actual.jpg', pageNumber: 1 }] }] };
    localStorage.setItem(DEMO_STORAGE_KEY, JSON.stringify(workspace));
    const repository = createDemoRepository();
    await repository.checkExams([exams[0].id], defaultCriteria());
    vi.advanceTimersByTime(4000);
    expect(await repository.getExam(exams[0].id)).toMatchObject({ status: 'completed', source: 'upload' });
    expect((await repository.getExam(exams[0].id)).result).toBeUndefined();
  });

  it('preserves photo-only tasks for a blank bulk task and allows an explicit shared photo to replace them', async () => {
    const { repository, exams } = await samples();
    await repository.saveExam(exams[0].id, { criteria: { ...defaultCriteria(), taskText: '', taskPhoto: jpeg, taskSource: 'photo' } });
    await repository.checkExams([exams[0].id, exams[1].id], defaultCriteria());
    expect((await repository.getExam(exams[0].id)).criteria).toMatchObject({ taskText: '', taskPhoto: jpeg, taskSource: 'photo' });
    vi.advanceTimersByTime(4000);
    await repository.load();
    const replacement = 'data:image/jpeg;base64,ZGVm';
    await repository.checkExams([exams[0].id], { ...defaultCriteria(), taskPhoto: replacement, taskSource: 'photo' });
    expect((await repository.getExam(exams[0].id)).criteria.taskPhoto).toBe(replacement);
  });

  it('allows a single exam check to clear its existing task text and photo', async () => {
    const { repository, exams } = await samples();
    await repository.saveExam(exams[0].id, { criteria: { ...defaultCriteria(), taskText: 'Previous task', taskPhoto: jpeg, taskSource: 'photo' } });
    await repository.checkExams([exams[0].id], { ...defaultCriteria(), detectTask: false });
    const exam = await repository.getExam(exams[0].id);
    expect(exam.criteria.taskText).toBe('');
    expect(exam.criteria.taskSource).toBe('');
    expect(exam.criteria.taskPhoto).toBeUndefined();
  });

  it('rejects invalid selections atomically and keeps other folders when deleting', async () => {
    const { repository, exams, folder } = await samples();
    await repository.createFolder('Another class');
    const otherFolder = (await repository.load()).folders[1];
    await repository.addSamples(otherFolder.id);
    const otherExam = (await repository.load()).exams.find(exam => exam.folderId === otherFolder.id)!;
    const before = localStorage.getItem(DEMO_STORAGE_KEY);
    await expect(repository.checkExams([exams[0].id, otherExam.id], defaultCriteria())).rejects.toThrow('one folder');
    expect(localStorage.getItem(DEMO_STORAGE_KEY)).toBe(before);
    await repository.deleteFolder(folder.id);
    expect((await repository.load()).exams).toHaveLength(3);
    expect((await repository.load()).exams.every(exam => exam.folderId === otherFolder.id)).toBe(true);
  });

  it('surfaces storage failures without claiming or retaining an unsaved edit', async () => {
    const { repository, exams } = await samples();
    const saved = localStorage.getItem(DEMO_STORAGE_KEY);
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('Full', 'QuotaExceededError'); });
    await expect(repository.saveExam(exams[0].id, { studentName: 'Changed' })).rejects.toThrow('could not be saved');
    setItem.mockRestore();
    expect(localStorage.getItem(DEMO_STORAGE_KEY)).toBe(saved);
    expect((await createDemoRepository().getExam(exams[0].id)).studentName).toBe('Anna');
  });
});
