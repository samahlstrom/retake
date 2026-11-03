// The folder list that picks your videos folder (spec/setup.md): GET <endpoint>?path=P → the folders in P.

export interface FolderList {
  /** The folder, with ~ for the home folder. */
  path: string;
  /** The folder above it; "" at the top of the disk. */
  parent: string;
  folders: string[];
}

/** A folder inside another, as the server takes it: "~" + "/Clips" → "~/Clips", "/" + "x" → "/x". */
export function join(dir: string, name: string): string {
  return dir.endsWith('/') ? dir + name : `${dir}/${name}`;
}

/** GET the list, or throw a one-line reason. */
export async function listFolders(endpoint: string, path: string, f: typeof fetch = fetch): Promise<FolderList> {
  let r: Response;
  try {
    r = await f(`${endpoint}?path=${encodeURIComponent(path)}`, { cache: 'no-store' });
  } catch {
    throw new Error('Retake isn’t answering');
  }
  if (r.status === 404) throw new Error('That folder isn’t there any more');
  if (!r.ok) throw new Error('Can’t read that folder');
  const b = (await r.json().catch(() => ({}))) as Partial<FolderList>;
  return {
    path: typeof b.path === 'string' ? b.path : path,
    parent: typeof b.parent === 'string' ? b.parent : '',
    folders: Array.isArray(b.folders) ? b.folders.filter((x): x is string => typeof x === 'string') : [],
  };
}
