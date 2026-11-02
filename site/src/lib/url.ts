// A site path with the base the site is served under (/leeroy on GitHub Pages).
export const url = (path: string) => `${import.meta.env.BASE_URL.replace(/\/$/, '')}${path}`;

export const REPO = 'https://github.com/pthm/leeroy';
