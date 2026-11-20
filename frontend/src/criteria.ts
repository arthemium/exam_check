import type { Check, Criteria } from './types';

export const checkOptions: { value: Check; label: string; description: string }[] = [
  { value: 'grammar', label: 'Grammar', description: 'Tenses, agreement, and sentence structure' },
  { value: 'spelling', label: 'Spelling', description: 'Misspelled words and typos' },
  { value: 'punctuation', label: 'Punctuation', description: 'Commas, full stops, and apostrophes' },
  { value: 'structure', label: 'Essay structure', description: 'Paragraphs, flow, and organisation' },
  { value: 'format', label: 'Format', description: 'Conventions of the requested text type' },
  { value: 'register', label: 'Register', description: 'Formality and tone for the reader' },
  { value: 'style', label: 'Style', description: 'Clarity and consistent expression' },
  { value: 'taskCompletion', label: 'Task completion', description: 'Topic coverage and how fully the reader is informed' },
];
export const basicChecks: Check[] = ['grammar', 'spelling', 'punctuation', 'structure'];
export const defaultCriteria = (): Criteria => ({ checks: [...basicChecks], assessment: 'general', customCriteria: '', minWords: '', maxWords: '', detectTask: true, taskText: '', taskSource: '' });
export const isChecking = (status: string) => status === 'queued' || status === 'processing';
export function criteriaError(criteria: Criteria): string {
  const limits = [criteria.minWords, criteria.maxWords];
  if (limits.some(value => value !== '' && (!/^[1-9]\d*$/.test(value) || Number(value) > 10000))) return 'Use whole numbers from 1 to 10,000, or leave the limits blank.';
  if (criteria.minWords && criteria.maxWords && Number(criteria.minWords) > Number(criteria.maxWords)) return 'Maximum words must be at least the minimum.';
  if (!criteria.checks.length && criteria.assessment !== 'cae' && !criteria.customCriteria.trim() && !criteria.minWords && !criteria.maxWords) return 'Choose at least one criterion.';
  return '';
}
