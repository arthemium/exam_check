import { Modal } from './Modal';
interface Props { label: string; description: string; busy: boolean; onClose: () => void; onDelete: () => Promise<boolean> }
export function DeleteDialog({ label, description, busy, onClose, onDelete }: Props) {
  return <Modal title={`Delete ${label}?`} onClose={onClose} busy={busy}><div className="dialog-form">
    <p>{description} This cannot be undone.</p>
    <div className="inline-actions"><button className="button" disabled={busy} onClick={onClose} autoFocus>Cancel</button><button className="button danger" disabled={busy} onClick={async () => { if (await onDelete()) onClose(); }}>{busy ? 'Deleting…' : 'Delete'}</button></div>
  </div></Modal>;
}
