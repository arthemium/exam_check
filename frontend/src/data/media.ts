import type { Photo } from '../types';

export const maxPhotoSourceBytes = 100 * 1024 * 1024;

export function validatePhotoFile(file: File): void {
  if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) throw new Error('Choose a JPEG, PNG, or WebP photo.');
  if (!file.size || file.size > maxPhotoSourceBytes) throw new Error('Each original photo must be at most 100 MiB and must not be empty.');
}

function readDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    const timer = setTimeout(() => { reader.abort(); reject(new Error('The photo took too long to read.')); }, 15000);
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('This photo could not be read.'));
    reader.onerror = () => reject(new Error('This photo could not be read.'));
    reader.onabort = () => reject(new Error('Reading the photo was cancelled.'));
    reader.onloadend = () => clearTimeout(timer);
    try { reader.readAsDataURL(file); }
    catch { clearTimeout(timer); reject(new Error('This photo could not be read.')); }
  });
}

function decodePhoto(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const photo = new Image();
    const finish = (error?: Error) => {
      clearTimeout(timer);
      photo.onload = photo.onerror = null;
      if (error) { photo.src = ''; reject(error); } else resolve(photo);
    };
    const timer = setTimeout(() => finish(new Error('The photo took too long to open. Try a smaller image.')), 15000);
    photo.onload = () => finish();
    photo.onerror = () => finish(new Error('The image could not be opened. Try another photo.'));
    photo.src = url;
  });
}

function photoCanvas(photo: HTMLImageElement, longestSide: number): HTMLCanvasElement {
  const scale = Math.min(1, longestSide / Math.max(photo.naturalWidth, photo.naturalHeight));
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(photo.naturalWidth * scale));
  canvas.height = Math.max(1, Math.round(photo.naturalHeight * scale));
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Photo previews are not available in this browser.');
  context.fillStyle = '#ffffff';
  context.fillRect(0, 0, canvas.width, canvas.height);
  context.drawImage(photo, 0, 0, canvas.width, canvas.height);
  return canvas;
}

function encodePreview(photo: HTMLImageElement): string {
  for (const side of [1600, 1280, 1024]) {
    const canvas = photoCanvas(photo, side);
    try {
      for (const quality of [0.84, 0.72, 0.6]) {
        const url = canvas.toDataURL('image/jpeg', quality);
        if (url.startsWith('data:image/jpeg;base64,') && url.length <= 450000) return url;
      }
    } finally { canvas.width = canvas.height = 0; }
  }
  throw new Error('This photo is too detailed for the preview. Try a smaller image.');
}

export async function preparePhoto(file: File): Promise<Photo> {
  validatePhotoFile(file);
  const photo = await decodePhoto(await readDataUrl(file));
  try {
    const pixels = photo.naturalWidth * photo.naturalHeight;
    if (!Number.isSafeInteger(pixels) || pixels < 1 || pixels > 20000000) throw new Error('Choose a photo with no more than 20 megapixels.');
    return { id: crypto.randomUUID(), url: encodePreview(photo), fileName: file.name.slice(0, 120), pageNumber: 1 };
  } finally { photo.src = ''; }
}

export async function prepareTaskPhoto(file: File): Promise<Photo> {
  validatePhotoFile(file);
  return { id: crypto.randomUUID(), url: await readDataUrl(file), fileName: file.name.slice(0, 120), pageNumber: 1 };
}
