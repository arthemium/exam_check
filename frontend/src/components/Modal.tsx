import { useEffect, useRef, type ReactNode } from 'react';

interface Props { title: string; onClose: () => void; children: ReactNode; busy?: boolean; className?: string }
export function Modal({ title, onClose, children, busy = false, className = '' }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const headingId = 'dialog-heading';
  useEffect(() => {
    const element = dialog.current;
    const previous = document.activeElement as HTMLElement | null;
    if (element?.showModal) element.showModal(); else element?.setAttribute('open', '');
    element?.querySelector<HTMLInputElement>('input')?.focus();
    return () => previous?.focus();
  }, []);
  return <dialog ref={dialog} className={`modal ${className}`} aria-labelledby={headingId} onCancel={event => { event.preventDefault(); if (!busy) onClose(); }}>
    <div className="panel-header"><h2 id={headingId}>{title}</h2><button className="button quiet" onClick={onClose} disabled={busy} aria-label="Close dialog">Close</button></div>
    {children}
  </dialog>;
}
