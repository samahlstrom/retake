// The linked-folder API. The page keeps its draft locally; this module only sends
// the server's small manifest shape.
export interface LinkedFolder {
  id: string;
  path: string;
  subfolders: boolean;
  available: boolean;
}

export interface Library {
  computer: { name: string; platform: string };
  native_picker: boolean;
  revision: number;
  folders: LinkedFolder[];
}

async function reply(r: Response): Promise<Library> {
  const body = (await r.json().catch(() => ({}))) as Library & { error?: unknown };
  if (!r.ok)
    throw new Error(typeof body.error === 'string' && body.error ? body.error : 'Couldn’t save linked folders.');
  return body;
}

/** Reads the server-owned folder list. */
export async function loadLibrary(fetcher: typeof fetch = fetch): Promise<Library> {
  try {
    return await reply(await fetcher('api/library', { cache: 'no-store' }));
  } catch (e) {
    if (e instanceof Error) throw e;
    throw new Error('The server isn’t answering', { cause: e });
  }
}

/** Replaces the list, without sending display-only availability state. */
export async function saveLibrary(
  revision: Library['revision'],
  folders: LinkedFolder[],
  fetcher: typeof fetch = fetch,
): Promise<Library> {
  try {
    return await reply(
      await fetcher('api/library', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          revision,
          folders: folders.map(({ id, path, subfolders }) => ({ id, path, subfolders })),
        }),
      }),
    );
  } catch (e) {
    if (e instanceof Error) throw e;
    throw new Error('The server isn’t answering', { cause: e });
  }
}
