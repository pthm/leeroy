import rss from '@astrojs/rss';
import type { APIContext } from 'astro';
import { getCollection } from 'astro:content';
import { url } from '../lib/url';

export async function GET(context: APIContext) {
  const posts = (await getCollection('blog')).toSorted((a, b) => +b.data.date - +a.data.date);
  return rss({
    title: 'LEEROY',
    description: 'Stories from LEEROY runs and how the benchmark is built.',
    site: context.site!,
    items: posts.map(p => ({ title: p.data.title, description: p.data.description, pubDate: p.data.date, link: url(`/blog/${p.id}`) })),
  });
}
