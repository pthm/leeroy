import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';
import icon from 'astro-icon';
import { defineConfig } from 'astro/config';
import noir from './src/data/shiki/terminal-noir.json' with { type: 'json' };

// The site is https://leeroy.dev, at the domain's root. SITE_URL and SITE_BASE override them (a preview elsewhere).
export default defineConfig({
  site: process.env.SITE_URL || 'https://leeroy.dev',
  base: process.env.SITE_BASE ?? '/',
  output: 'static',
  trailingSlash: 'ignore',
  integrations: [icon(), sitemap()],
  markdown: {
    shikiConfig: { theme: noir },
  },
  build: { format: 'directory', inlineStylesheets: 'always' },
  vite: { plugins: [tailwindcss()] },
});
