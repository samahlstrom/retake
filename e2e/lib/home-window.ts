// The home page's test hook (window.__home), declared once for every spec that reads it.
export interface HomeCard {
  name: string;
  version: string;
  status: string;
  thumb: string;
}

declare global {
  interface Window {
    __home: {
      loaded: boolean;
      projects: HomeCard[];
      error: string;
      selected: string;
    };
  }
}

// The setup page's test hook (window.__setup, spec/setup.md).
declare global {
  interface Window {
    __setup: { loaded: boolean; done: boolean };
  }
}
