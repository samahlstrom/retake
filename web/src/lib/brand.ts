// The theme's name and logo (spec/theme.md, "How the page loads it"). The server adds two metas to the page when the
// theme folder's theme.toml sets them; without a theme the name is "Retake" and there is no logo.

export interface Brand {
  /** Shown in the titles and at the top of the home. */
  name: string;
  /** The logo's URL (theme/<file>, relative like the page's own), or "" for none. */
  logo: string;
  /** Whether a theme named it (the review page's title says the name only then). */
  custom: boolean;
}

export const DEFAULT_NAME = 'Retake';

/** The brand the page was served with. */
export function brandOf(doc: Pick<Document, 'querySelector'> | undefined = globalThis.document): Brand {
  const meta = (name: string) => doc?.querySelector<HTMLMetaElement>(`meta[name="${name}"]`)?.content.trim() ?? '';
  const name = meta('retake-brand');
  return { name: name || DEFAULT_NAME, logo: meta('retake-logo'), custom: !!name };
}

/** The home's title: "Projects · Retake", "Launch · Acme Review". */
export const homeTitle = (title: string, brand: Brand): string => `${title} · ${brand.name}`;

/** The review page's title: "Review · clip", and the theme's name after it when it has one. */
export const reviewTitle = (video: string, brand: Brand): string =>
  brand.custom ? `Review · ${video} · ${brand.name}` : `Review · ${video}`;
