export type Check = 'grammar' | 'spelling' | 'punctuation' | 'structure' | 'format' | 'register' | 'style' | 'taskCompletion';
export type ExamStatus = 'uploaded' | 'queued' | 'processing' | 'completed' | 'failed';
export interface Criteria {
  checks: Check[];
  assessment: 'general' | 'b2_first_schools';
  customCriteria: string;
  minWords: string;
  maxWords: string;
  detectTask: boolean;
  taskText: string;
  taskSource: '' | 'essay' | 'photo' | 'manual';
  taskPhoto?: string;
}
export interface Folder { id: string; name: string; examCount?: number }
export interface Photo { id: string; url: string; fileName: string; pageNumber: number }
export interface Paragraph {
  id: string; text: string; pageNumber: number;
  uncertainRanges?: { start: number; end: number; certainty: string }[];
}
export interface Issue {
  id: string; category: Check; scope: 'span' | 'paragraph' | 'document';
  paragraphId?: string | null; quote?: string | null; start?: number | null; end?: number | null;
  suggestion: string; explanation: string;
}
export interface Assessment { criterion: string; feedback: string; score: number | null }
export interface ExamResult {
  assessment?: Criteria['assessment'] | 'cae';
  paragraphs: Paragraph[]; issues: Issue[]; needsTranscriptionReview?: boolean; warnings?: string[];
  task?: { text: string; source: Criteria['taskSource'] };
  wordCount?: number;
  wordLimit?: { minimum: number | null; maximum: number | null; status: 'within' | 'below' | 'above' | 'not_set' };
  assessments?: Assessment[];
  customAssessments?: { criterion: string; feedback: string }[];
}
export interface Exam {
  id: string; folderId: string; title: string; studentName: string;
  status: ExamStatus; criteria: Criteria; photos: Photo[]; source: 'sample' | 'upload';
  result?: ExamResult; error?: string; progress?: number;
  batchId?: string; stage?: string; demoStartedAt?: number;
}
export interface Workspace { folders: Folder[]; exams: Exam[] }
export interface Session { username: string; displayName: string }
export interface Capabilities { advancedCriteria: boolean; delete: boolean; taskPhoto: boolean; demo: boolean }
export interface ExamChanges { title?: string; studentName?: string; criteria?: Criteria }
export interface Repository {
  readonly capabilities: Capabilities;
  session(): Promise<Session | null>;
  login(username: string, password: string): Promise<Session>;
  logout(): Promise<void>;
  load(): Promise<Workspace>;
  getExam(id: string): Promise<Exam>;
  createFolder(name: string): Promise<void>;
  renameFolder(id: string, name: string): Promise<void>;
  deleteFolder(id: string): Promise<void>;
  upload(folderId: string, file: File): Promise<void>;
  addSamples(folderId: string): Promise<void>;
  saveExam(id: string, changes: ExamChanges): Promise<void>;
  deleteExam(id: string): Promise<void>;
  checkExams(ids: string[], criteria: Criteria): Promise<void>;
}
