import { useRef, useState, type ChangeEvent } from 'react';
import type { Capabilities, Criteria } from '../types';
import { preparePhoto, prepareTaskPhoto } from '../data/media';
import { sampleTask } from '../data/sample';

interface TaskInstructionsProps {
  criteria: Criteria;
  capabilities: Capabilities;
  bulk: boolean;
  disabled: boolean;
  onChange: (patch: Partial<Criteria>) => void;
  onBusyChange: (busy: boolean) => void;
}

export function TaskInstructions({ criteria, capabilities, bulk, disabled, onChange, onBusyChange }: TaskInstructionsProps) {
  const upload = useRef<HTMLInputElement>(null);
  const camera = useRef<HTMLInputElement>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const unavailable = !capabilities.taskPhoto;
  async function addPhoto(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file || disabled || unavailable) return;
    setLoading(true);
    onBusyChange(true);
    setError('');
    try {
      const photo = await (capabilities.demo ? preparePhoto(file) : prepareTaskPhoto(file));
      onChange({ taskPhoto: photo.url });
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'This photo could not be opened.'); }
    finally { setLoading(false); onBusyChange(false); }
  }
  return <section className="panel task-panel" aria-label="Task instructions">
    <div className="panel-header"><h2>Task instructions</h2><span className="muted">Optional</span></div>
    <div className="panel-body">
      <p className="helper">{bulk ? "Leave blank to keep each exam's task. Entering a shared task replaces the instructions for all selected exams." : "If the task is on a separate page, add a photo or type the instructions here."}</p>
      {unavailable && <p className="notice">Task transcription is not available on this server yet.</p>}
      <div className="inline-actions">
        <button type="button" className="button" disabled={disabled || loading || unavailable} onClick={() => camera.current?.click()}>Take task photo</button>
        <button type="button" className="button" disabled={disabled || loading || unavailable} onClick={() => upload.current?.click()}>Upload task photo</button>
      </div>
      <input ref={upload} type="file" accept="image/jpeg,image/png,image/webp" hidden onChange={addPhoto} />
      <input ref={camera} type="file" accept="image/jpeg,image/png,image/webp" capture="environment" hidden onChange={addPhoto} />
      {loading && <p className="helper" role="status">Preparing task photo…</p>}
      {error && <p className="form-error" role="alert">{error}</p>}
      {criteria.taskPhoto && <div className="task-photo-preview"><img src={criteria.taskPhoto} alt="Task instructions photo" /><button type="button" className="button" disabled={disabled || loading} onClick={() => onChange({ taskPhoto: undefined })}>Remove task photo</button></div>}
      <label className="field">{bulk ? 'Shared task transcription' : 'Task transcription'}<textarea rows={6} maxLength={4000} disabled={disabled || unavailable} value={criteria.taskText} onChange={event => onChange({ taskText: event.target.value, taskSource: event.target.value.trim() ? 'manual' : '' })} placeholder={bulk ? 'Leave blank to keep individual instructions' : 'Review or type the task instructions'} /></label>
      {capabilities.demo && <><p className="helper">Photo text is not extracted in this preview. You can type it above.</p><button type="button" className="button quiet" disabled={disabled || loading} onClick={() => onChange({ taskText: sampleTask, taskSource: 'essay' })}>Use sample task</button></>}
      {!capabilities.demo && !unavailable && <p className="helper">The task photo is transcribed when you check the exam. Typed instructions take priority over photo text.</p>}
      <p className="helper">Task instructions are kept separate from the student's answer.</p>
    </div>
  </section>;
}
