import { describe, expect, it, vi } from 'vitest';
import { createApiRepository } from './api';

const signedIn = () => new Response(JSON.stringify({ username: 'teacher', display_name: 'Teacher' }));

describe('API request recovery', () => {
  it('times out stalled requests, aborts them, and permits retry', async () => {
    let signal: AbortSignal | null | undefined;
    const send = vi.fn<typeof fetch>(async (_url, init) => {
      signal = init?.signal;
      return new Promise<Response>(() => {});
    });
    const repository = createApiRepository({ fetch: send, requestTimeoutMs: 5 });

    await expect(repository.session()).rejects.toMatchObject({ status: 0, message: expect.stringContaining('too long') });
    expect(signal?.aborted).toBe(true);
    send.mockResolvedValueOnce(signedIn());
    await expect(repository.session()).resolves.toEqual({ username: 'teacher', displayName: 'Teacher' });
  });

  it('also bounds response body reads after headers have arrived', async () => {
    const send = vi.fn<typeof fetch>(async () => new Response(new ReadableStream()));
    const repository = createApiRepository({ fetch: send, requestTimeoutMs: 5 });

    await expect(repository.session()).rejects.toMatchObject({ status: 0, message: expect.stringContaining('too long') });
    send.mockResolvedValueOnce(signedIn());
    await expect(repository.session()).resolves.toMatchObject({ username: 'teacher' });
  });

  it('gives large uploads a separate, longer response deadline', async () => {
    const send = vi.fn<typeof fetch>(async () => {
      await new Promise(resolve => setTimeout(resolve, 15));
      return new Response('{}', { status: 201 });
    });
    const repository = createApiRepository({ fetch: send, requestTimeoutMs: 5, uploadTimeoutMs: 100 });

    await expect(repository.upload('folder', new File(['image'], 'photo.jpg', { type: 'image/jpeg' }))).resolves.toBeUndefined();
    expect(send.mock.calls[0][1]?.signal?.aborted).toBe(false);
  });
});
