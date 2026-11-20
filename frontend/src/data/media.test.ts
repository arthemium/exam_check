import { describe, expect, it, vi } from 'vitest';
import { maxPhotoSourceBytes, prepareTaskPhoto, validatePhotoFile } from './media';

function photoWithSize(size: number): File {
  const file = new File(['photo'], 'page.png', { type: 'image/png' });
  Object.defineProperty(file, 'size', { value: size });
  return file;
}

describe('original photo uploads', () => {
  it('accepts originals above 10 MiB through the source-upload ceiling', () => {
    for (const size of [10 * 1024 * 1024 + 1, 50 * 1024 * 1024, maxPhotoSourceBytes]) {
      expect(() => validatePhotoFile(photoWithSize(size))).not.toThrow();
    }
  });

  it('rejects empty, oversized, and unsupported original files', () => {
    for (const size of [0, maxPhotoSourceBytes + 1]) {
      expect(() => validatePhotoFile(photoWithSize(size))).toThrow('100 MiB');
    }
    expect(() => validatePhotoFile(new File(['document'], 'page.pdf', { type: 'application/pdf' }))).toThrow('JPEG, PNG, or WebP');
  });

  it('preserves original task-photo bytes without drawing or recompressing them', async () => {
    const bytes = new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10, 0, 255, 42]);
    const file = new File([bytes], 'task.png', { type: 'image/png' });
    const canvas = vi.spyOn(HTMLCanvasElement.prototype, 'getContext');
    try {
      const photo = await prepareTaskPhoto(file);
      const expected = btoa(String.fromCharCode(...bytes));
      expect(photo).toMatchObject({ url: `data:image/png;base64,${expected}`, fileName: 'task.png', pageNumber: 1 });
      expect(canvas).not.toHaveBeenCalled();
    } finally { canvas.mockRestore(); }
  });
});
