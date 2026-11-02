// A site path with the base the site is served under (/leeroy on GitHub Pages).
export const url = (path: string) => `${import.meta.env.BASE_URL.replace(/\/$/, '')}${path}`;

export const REPO = 'https://github.com/pthm/leeroy';

// The results pages for a version: the latest has the plain addresses (/results, and /results/<task> for a task's
// latest version); the others /results/v/<version>/...
export function resultsUrl(version: string, task?: string, latest?: string): string {
  if (!task) return url(version === latest ? '/results' : `/results/v/${version}`);
  return url(version === latest ? `/results/${task}` : `/results/v/${version}/${task}`);
}
