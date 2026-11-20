import { useState, type FormEvent } from 'react';
import { Modal } from './Modal';

interface Props { title: string; name?: string; studentName?: string; exam?: boolean; busy: boolean; onClose: () => void; onSave: (name: string, studentName: string) => Promise<boolean> }
export function NameDialog(props: Props) {
  const [name, setName] = useState(props.name || '');
  const [student, setStudent] = useState(props.studentName || '');
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim() && await props.onSave(name.trim(), student.trim())) props.onClose();
  }
  return <Modal title={props.title} onClose={props.onClose} busy={props.busy}><form onSubmit={submit} className="dialog-form">
    {props.exam && <label className="field">Student name<input value={student} maxLength={120} onChange={event => setStudent(event.target.value)} /></label>}
    <label className="field">{props.exam ? 'Exam title' : 'Folder name'}<input value={name} maxLength={120} required onChange={event => setName(event.target.value)} /></label>
    <div className="inline-actions"><button className="button primary" disabled={props.busy || !name.trim()}>{props.busy ? 'Saving…' : 'Save'}</button><button type="button" className="button" onClick={props.onClose} disabled={props.busy}>Cancel</button></div>
  </form></Modal>;
}
