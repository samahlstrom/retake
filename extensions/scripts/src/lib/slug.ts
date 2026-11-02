// A new idea's folder name (spec/scripts.md, "New idea"): the title in lower case with every run of other characters
// turned into "-", at most 63 characters, and "-2", "-3", … when the folder exists.

export const MAX_SLUG = 63;

/** "Jump to Secure Souls!" is "jump-to-secure-souls". Accents are dropped first (café is cafe); a title with no
 * letters or digits is "idea". */
export function slug(title: string): string {
  const s = title
    .normalize('NFKD')
    .replace(/\p{M}+/gu, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+/, '');
  return s.slice(0, MAX_SLUG).replace(/-+$/, '') || 'idea';
}

/** The nth try's name: the slug itself for n = 1, else "<slug>-<n>", cut so the whole stays within 63 characters. */
export function nth(base: string, n: number): string {
  if (n <= 1) return base;
  const suffix = `-${n}`;
  return `${base.slice(0, MAX_SLUG - suffix.length).replace(/-+$/, '')}${suffix}`;
}
