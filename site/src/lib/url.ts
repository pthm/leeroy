// A site path with the base the site is served under (/leeroy on GitHub Pages).
export const url = (path: string) => `${import.meta.env.BASE_URL.replace(/\/$/, '')}${path}`;

export const REPO = 'https://github.com/pthm/leeroy';

// The results pages for a version: the latest has the plain addresses (/results, and /results/<task> for a task's
// latest version); the others /results/v/<version>/...
export function resultsUrl(version: string, task?: string, latest?: string): string {
  if (!task) return url(version === latest ? '/results' : `/results/v/${version}`);
  return url(version === latest ? `/results/${task}` : `/results/v/${version}/${task}`);
}

// A published run's file (an icon, a map image, a download), at its path in the assets bucket: PUBLIC_ASSETS_URL,
// https://assets.leeroy.dev in production; in development a path the dev server serves var/assets under
// (public/assets links to it), so a run can be checked before just upload. Empty when there is no assets URL.
const assetsBase = (import.meta.env.PUBLIC_ASSETS_URL ?? '').replace(/\/$/, '');
export const hasAssets = assetsBase !== '';
export const asset = (path: string) => (assetsBase.startsWith('/') ? url(`${assetsBase}/${path}`) : `${assetsBase}/${path}`);
