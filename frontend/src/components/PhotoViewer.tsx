import { useState, type CSSProperties } from 'react';
import type { Exam, Photo } from '../types';
import { Modal } from './Modal';

export function SamplePage({ exam }: { exam: Exam }) {
  const paragraphs = exam.result?.paragraphs;
  return <article className="sample-page" aria-label="Illustrated sample exam page">
    {exam.criteria.taskText && <div className="sample-task"><strong>Task</strong><p>{exam.criteria.taskText}</p></div>}
    <h3>{exam.title}</h3>
    {paragraphs?.length ? paragraphs.map(paragraph => <p key={paragraph.id}>{paragraph.text}</p>) : <>
      <p>Learning a new language opens doors to different cultures and helps people communicate with confidence.</p>
      <p>My sister practice English every evening. Small daily efforts can make a big differance.</p>
    </>}
    <small>Sample page · {exam.studentName || 'Student'}</small>
  </article>;
}

function PhotoPageSelect({ photos, pageIndex, onSelect }: { photos: Photo[]; pageIndex: number; onSelect: (index: number) => void }) {
  if (photos.length < 2) return null;
  return <label>Page <select aria-label="Photo page" value={pageIndex} onChange={event => onSelect(Number(event.target.value))}>
    {photos.map((page, index) => <option value={index} key={page.id}>Page {page.pageNumber}</option>)}
  </select></label>;
}

function PhotoContent({ photo, exam }: { photo?: Photo; exam: Exam }) {
  if (photo) return <img src={photo.url} alt={`Exam photo: ${exam.studentName || exam.title}, page ${photo.pageNumber}`} />;
  if (exam.source === 'sample') return <SamplePage exam={exam} />;
  return <p className="empty-state">No photo preview is available.</p>;
}

export function PhotoViewer({ photos, exam, compact = false }: { photos: Photo[]; exam: Exam; compact?: boolean }) {
  const [pageIndex, setPageIndex] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const [zoom, setZoom] = useState(100);
  const currentPage = Math.max(0, Math.min(pageIndex, photos.length - 1));
  const photo = photos[currentPage];
  const caption = photo?.fileName || (exam.source === 'sample' ? 'Sample page illustration' : 'Exam photo');
  function selectPage(index: number) { setPageIndex(index); setZoom(100); }
  function closePhoto() { setExpanded(false); setZoom(100); }
  return <section className={`panel photo-viewer ${compact ? 'compact' : ''}`} aria-label="Exam photo">
    <div className="panel-header"><h2>Exam photo</h2><div className="photo-controls">
      <PhotoPageSelect photos={photos} pageIndex={currentPage} onSelect={selectPage} />
      <button type="button" className="button" aria-haspopup="dialog" aria-expanded={expanded} disabled={!photo && exam.source !== 'sample'} onClick={() => setExpanded(true)}>Enlarge photo</button>
    </div></div>
    <div className="photo-stage"><PhotoContent photo={photo} exam={exam} /></div>
    <p className="helper photo-caption">{caption}</p>
    {expanded && <Modal title="Original exam photo" className="photo-modal" onClose={closePhoto}>
      <div className="photo-controls photo-toolbar">
        <PhotoPageSelect photos={photos} pageIndex={currentPage} onSelect={selectPage} />
        <button type="button" className="button" disabled={zoom === 100} onClick={() => setZoom(value => value - 50)}>Zoom out</button>
        <output aria-label="Photo zoom" aria-live="polite">{zoom}%</output>
        <button type="button" className="button" disabled={zoom === 300} onClick={() => setZoom(value => value + 50)}>Zoom in</button>
        <button type="button" className="button" onClick={() => setZoom(100)}>Fit photo</button>
      </div>
      <div className="photo-zoom-stage" tabIndex={0} aria-label="Enlarged photo; scroll to inspect when zoomed">
        <div className="photo-zoom-content" style={{ width: `${zoom}%`, '--photo-zoom': zoom / 100 } as CSSProperties}><PhotoContent photo={photo} exam={exam} /></div>
      </div>
      <p className="helper photo-caption">{caption}</p>
    </Modal>}
  </section>;
}
