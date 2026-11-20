import type { Criteria, Issue, Paragraph } from '../types';

export interface TranscriptPart { text: string; issueIds: string[]; uncertain: boolean; start: number }

function validRange(start: unknown, end: unknown, length: number): start is number {
  return Number.isInteger(start) && Number.isInteger(end) && Number(start) >= 0 && Number(end) > Number(start) && Number(end) <= length;
}

export function segmentParagraph(paragraph: Paragraph, issues: Issue[]): TranscriptPart[] {
  const findings = issues.filter(issue => issue.scope === 'span' && issue.paragraphId === paragraph.id && validRange(issue.start, issue.end, paragraph.text.length) && (!issue.quote || paragraph.text.slice(issue.start!, issue.end!) === issue.quote));
  const uncertain = (paragraph.uncertainRanges || []).filter(range => validRange(range.start, range.end, paragraph.text.length));
  const boundaries = [...new Set([0, paragraph.text.length, ...findings.flatMap(issue => [issue.start!, issue.end!]), ...uncertain.flatMap(range => [range.start, range.end])])].sort((a, b) => a - b);
  return boundaries.slice(0, -1).map((start, index) => {
    const end = boundaries[index + 1];
    return {
      start, text: paragraph.text.slice(start, end),
      uncertain: uncertain.some(range => range.start <= start && range.end >= end),
      issueIds: findings.filter(issue => issue.start! <= start && issue.end! >= end).map(issue => issue.id),
    };
  });
}

export function countWords(paragraphs: Paragraph[]): number {
  return paragraphs.map(paragraph => paragraph.text).join(' ').split(/\s+/u).filter(word => /[\p{L}\p{N}]/u.test(word)).length;
}

export function wordCountMessage(count: number, criteria: Criteria): string {
  const minimum = Number(criteria.minWords);
  const maximum = Number(criteria.maxWords);
  if (criteria.minWords && count < minimum) return `${minimum - count} words below minimum (${minimum})`;
  if (criteria.maxWords && count > maximum) return `${count - maximum} words above maximum (${maximum})`;
  if (criteria.minWords && criteria.maxWords) return `Within ${minimum}–${maximum} words`;
  if (criteria.minWords) return `Meets minimum of ${minimum} words`;
  if (criteria.maxWords) return `Within maximum of ${maximum} words`;
  return 'No word limit set';
}
