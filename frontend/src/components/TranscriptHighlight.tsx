import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { Issue } from '../types';
import { checkOptions } from '../criteria';

const categoryLabel = (issue: Issue) => checkOptions.find(option => option.value === issue.category)?.label || issue.category;

function positionBubble(anchor: HTMLElement, bubble: HTMLElement) {
  const rect = anchor.getBoundingClientRect();
  const left = Math.max(12, Math.min(rect.left, window.innerWidth - bubble.offsetWidth - 12));
  const below = rect.bottom + 8;
  const preferredTop = below + bubble.offsetHeight <= window.innerHeight - 12 ? below : rect.top - bubble.offsetHeight - 8;
  const top = Math.max(12, Math.min(preferredTop, window.innerHeight - bubble.offsetHeight - 12));
  bubble.style.left = `${left}px`;
  bubble.style.top = `${top}px`;
  bubble.style.visibility = 'visible';
}

interface BubbleProps {
  id: string; anchor: HTMLButtonElement; issues: Issue[];
  onEnter: () => void; onLeave: () => void; onClose: () => void;
}

function SuggestionBubble({ id, anchor, issues, onEnter, onLeave, onClose }: BubbleProps) {
  const bubble = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const update = () => { if (bubble.current) positionBubble(anchor, bubble.current); };
    update();
    window.addEventListener('resize', update);
    window.addEventListener('scroll', update, true);
    return () => { window.removeEventListener('resize', update); window.removeEventListener('scroll', update, true); };
  }, [anchor, issues]);
  useEffect(() => {
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose(); };
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !anchor.contains(event.target) && !bubble.current?.contains(event.target)) onClose();
    };
    document.addEventListener('keydown', escape);
    document.addEventListener('pointerdown', outside);
    return () => { document.removeEventListener('keydown', escape); document.removeEventListener('pointerdown', outside); };
  }, [anchor, onClose]);
  return createPortal(<div ref={bubble} id={id} role="tooltip" className="suggestion-bubble" onMouseEnter={onEnter} onMouseLeave={onLeave}>
    {issues.map(issue => <section key={issue.id} data-check={issue.category}>
      <h3 className="eyebrow">{categoryLabel(issue)}</h3>
      {issue.quote && <p className="issue-quote">{issue.quote}</p>}
      <strong>{issue.suggestion}</strong>
      <p>{issue.explanation}</p>
    </section>)}
  </div>, document.body);
}

export function TranscriptHighlight({ text, issues, selectedId, onSelect }: { text: string; issues: Issue[]; selectedId: string; onSelect: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  const hovered = useRef(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const bubbleId = useId();
  const selected = issues.some(issue => issue.id === selectedId);
  const issue = issues.find(issue => issue.id === selectedId) || issues[0];
  useEffect(() => () => clearTimeout(closeTimer.current), []);
  function show() { clearTimeout(closeTimer.current); setOpen(true); }
  function close() { clearTimeout(closeTimer.current); setOpen(false); }
  function scheduleClose() {
    clearTimeout(closeTimer.current);
    closeTimer.current = setTimeout(() => { if (!hovered.current && document.activeElement !== anchor.current) close(); }, 150);
  }
  function enter() { hovered.current = true; show(); }
  function leave() { hovered.current = false; scheduleClose(); }
  return <span onMouseEnter={enter} onMouseLeave={leave}>
    <button ref={anchor} type="button" data-check={issue.category} className={`text-mark mark-${issue.category} ${selected ? 'selected' : ''}`} aria-pressed={selected} aria-describedby={open ? bubbleId : undefined} aria-label={`${categoryLabel(issue)} feedback for ${text}`} onFocus={show} onBlur={scheduleClose} onClick={() => { onSelect(issue.id); show(); }}>{text}</button>
    {open && anchor.current && <SuggestionBubble id={bubbleId} anchor={anchor.current} issues={issues} onEnter={enter} onLeave={leave} onClose={close} />}
  </span>;
}
