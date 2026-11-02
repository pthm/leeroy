import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';
import icon from 'astro-icon';
import { defineConfig } from 'astro/config';
import noir from './src/data/shiki/terminal-noir.json' with { type: 'json' };
import paper from './src/data/shiki/terminal-noir-paper.json' with { type: 'json' };

const base = process.env.SITE_BASE ?? '/leeroy';

// GitHub Pages serves the project at /leeroy; SITE_BASE=/ for a custom domain or local preview at the root.
export default defineConfig({
  site: process.env.SITE_URL || 'https://pthm.github.io',
  base,
  output: 'static',
  trailingSlash: 'ignore',
  integrations: [icon(), sitemap()],
  markdown: {
    shikiConfig: { themes: { dark: noir, light: paper }, defaultColor: false }, // colours as CSS variables, picked in global.css
  },
  build: { format: 'directory', inlineStylesheets: 'always' },
  vite: { plugins: [tailwindcss()] },
});
