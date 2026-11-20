import type { Check, ExamResult, Photo } from '../types';

export const sampleTask = 'Write an essay about the benefits of learning a new language. Explain two benefits, describe effective learning habits, and support your ideas with examples.';
const paragraphs = [
  'Learning a new language opens doors to different cultures and helps people communicate with confidence. It also makes travel more enjoyable because visitors can understand local customs and conversations. My sister practice English every evening, and her progress has encouraged me to study regularly.',
  'I usually read short stories and write down unfamiliar words. Watching films also improves my listening skills. Sharing these activities with friends makes the process enjoyable. Although learning takes patience, small daily efforts can make a big differance over time.',
];

export function sampleResult(checks: Check[]): ExamResult {
  const grammar = paragraphs[0].indexOf('practice');
  const spelling = paragraphs[1].indexOf('differance');
  return {
    paragraphs: paragraphs.map((text, index) => ({ id: `p${index + 1}`, pageNumber: 1, text })),
    issues: [
      { id: 'grammar-1', category: 'grammar' as const, scope: 'span' as const, paragraphId: 'p1', quote: 'practice', start: grammar, end: grammar + 8, suggestion: 'practices', explanation: '“My sister” is singular. Use “practices” in the present simple.' },
      { id: 'spelling-1', category: 'spelling' as const, scope: 'span' as const, paragraphId: 'p2', quote: 'differance', start: spelling, end: spelling + 10, suggestion: 'difference', explanation: 'The correct spelling is “difference”.' },
      { id: 'structure-1', category: 'structure' as const, scope: 'paragraph' as const, paragraphId: 'p1', suggestion: 'Group learning habits together in the second paragraph.', explanation: 'Moving the sentence about your sister keeps related ideas together.' },
    ].filter(issue => checks.includes(issue.category)),
  };
}

function wrap(text: string, width = 62): string[] {
  const lines: string[] = [''];
  for (const word of text.split(' ')) {
    if ((lines[lines.length - 1] + word).length > width) lines.push('');
    lines[lines.length - 1] += `${word} `;
  }
  return lines.map(line => line.trim());
}

export function samplePhoto(studentName: string): Photo {
  const lines = [`${studentName} — Learning a new language`, '', ...wrap(`Task: ${sampleTask}`), '', ...paragraphs.flatMap(paragraph => [...wrap(paragraph), ''])];
  const escape = (value: string) => value.replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;' })[character]!);
  const text = lines.map((line, index) => `<text x="45" y="${60 + index * 29}">${escape(line)}</text>`).join('');
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="760" height="1060" viewBox="0 0 760 1060"><rect width="760" height="1060" fill="#fff"/><g font-family="Georgia,serif" font-size="19" fill="#222">${text}</g></svg>`;
  return { id: crypto.randomUUID(), url: `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`, fileName: `${studentName} · Sample essay`, pageNumber: 1 };
}
