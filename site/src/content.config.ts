import { defineCollection } from 'astro:content';
import { z } from 'astro/zod';
import { glob } from 'astro/loaders';

const blog = defineCollection({
  loader: glob({ pattern: '**/*.{md,mdx}', base: './src/content/blog' }),
  schema: () =>
    z.object({
      title: z.string(),
      description: z.string(),
      date: z.coerce.date(),
      image: z.string().optional(), // its path in the assets bucket (asset()), e.g. media/x.jpg
      banner: z.boolean().default(false), // the image behind the post's header too (art, not a busy screenshot)
    }),
});

const docs = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/docs' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    order: z.number(), // place in the sidebar
  }),
});

// Hand-written notes for a run's page, named after its id (src/data/runs/<id>.json): a title, a lede, findings at a
// glance, and the story in the body.
const runs = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/runs' }),
  schema: z.object({
    title: z.string(),
    lede: z.string(),
    glance: z.array(z.object({ head: z.string(), body: z.string() })).default([]),
  }),
});

export const collections = { blog, docs, runs };
