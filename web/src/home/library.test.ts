import { expect, it, vi } from 'vitest';
import { loadLibrary, saveLibrary } from './library';

it('keeps the exact selected folders and revision, and reports a conflicting save', async () => {
  const folders = [
    { id: 'orchard', path: '/media/Orchard', subfolders: false, available: true },
    { id: 'workshop', path: '/drive/Workshop', subfolders: true, available: false },
  ];
  const view = { computer: { name: 'Studio tower', platform: 'linux' }, native_picker: false, revision: 3, folders };
  const fetcher = vi.fn(async () => new Response(JSON.stringify(view), { status: 200 }));
  expect(await loadLibrary(fetcher as typeof fetch)).toEqual(view);
  expect(fetcher).toHaveBeenLastCalledWith('api/library', expect.objectContaining({ cache: 'no-store' }));
  await saveLibrary(view.revision, folders, fetcher as typeof fetch);
  const [url, init] = fetcher.mock.calls.at(-1)! as unknown as [string, RequestInit];
  expect(url).toBe('api/library');
  expect(init.method).toBe('POST');
  expect(JSON.parse(String(init.body))).toEqual({
    revision: 3,
    folders: folders.map(({ id, path, subfolders }) => ({ id, path, subfolders })),
  });
  const conflict = vi.fn(
    async () => new Response(JSON.stringify({ error: 'Folders changed. Reload and try again.' }), { status: 409 }),
  );
  await expect(saveLibrary(3, [], conflict as typeof fetch)).rejects.toThrow('Folders changed. Reload and try again.');
  expect(folders[0].subfolders).toBe(false);
  expect(folders[1].available).toBe(false);
});
