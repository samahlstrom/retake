// The video's versions as api/info lists them: Start v1 shows while there's no cut (no version that isn't a kind).

export const START_V1 = 'Build the first cut (v1) from script.md.';

/** Whether api/info lists a cut (a version without `kind`); null when it lists no versions at all. */
export function hasCut(info: unknown): boolean | null {
  const versions = (info as { versions?: unknown } | null)?.versions;
  if (!Array.isArray(versions)) return null;
  return versions.some(
    (v) =>
      !!v &&
      typeof v === 'object' &&
      typeof (v as { label?: unknown }).label === 'string' &&
      !(v as { kind?: unknown }).kind,
  );
}
