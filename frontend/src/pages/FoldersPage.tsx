import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useApp } from '../appContext';
import type { Folder } from '../types';
import { NameDialog } from '../components/NameDialog';
import { DeleteDialog } from '../components/DeleteDialog';

export function FoldersPage() {
  const { data, repository } = useApp();
  const [editor, setEditor] = useState<Folder | 'new' | null>(null);
  const [deleting, setDeleting] = useState<Folder | null>(null);
  return <section>
    <div className="screen-heading"><div><span className="eyebrow">YOUR EXAM LIBRARY</span><h1>Folders</h1><p className="muted">Open a folder to see its students' exams.</p></div><button className="button primary" disabled={data.busy} onClick={() => setEditor('new')}>Create folder</button></div>
    {data.workspace.folders.length ? <ul className="panel item-list" aria-label="Folders">{data.workspace.folders.map(folder => <li key={folder.id} className="item-row"><Link className="item-link" to={`/folders/${folder.id}`}><span className="file-symbol" aria-hidden="true">▱</span><span className="item-copy"><strong>{folder.name}</strong><small>Open folder</small></span><span className="tag">{data.workspace.exams.filter(exam => exam.folderId === folder.id).length} exams</span></Link><div className="row-actions"><button className="button" disabled={data.busy} onClick={() => setEditor(folder)}>Rename</button>{repository.capabilities.delete && <button className="button danger" disabled={data.busy} onClick={() => setDeleting(folder)}>Delete</button>}</div></li>)}</ul> : <div className="panel empty-state"><h2>Start with a folder</h2><p>Create a folder, then add the exams that belong there.</p><button className="button" onClick={() => setEditor('new')}>Create your first folder</button></div>}
    {repository.capabilities.demo && <p className="helper page-note">Demo data and photo previews are saved in this browser.</p>}
    {editor && <NameDialog title={editor === 'new' ? 'Create folder' : 'Rename folder'} name={editor === 'new' ? '' : editor.name} busy={data.busy} onClose={() => setEditor(null)} onSave={name => data.run(() => editor === 'new' ? repository.createFolder(name) : repository.renameFolder(editor.id, name))} />}
    {deleting && <DeleteDialog label="folder" description={`“${deleting.name}” and its ${data.workspace.exams.filter(exam => exam.folderId === deleting.id).length} exams will be removed.`} busy={data.busy} onClose={() => setDeleting(null)} onDelete={() => data.run(() => repository.deleteFolder(deleting.id))} />}
  </section>;
}
