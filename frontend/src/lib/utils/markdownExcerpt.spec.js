import { describe, expect, it } from 'vitest';
import { markdownExcerpt } from './markdownExcerpt.js';

describe('markdownExcerpt', () => {
  it('returns an empty string when there is no text', () => {
    expect(markdownExcerpt(null)).toBe('');
    expect(markdownExcerpt(undefined)).toBe('');
    expect(markdownExcerpt('')).toBe('');
    expect(markdownExcerpt('   \n\n  ')).toBe('');
    expect(markdownExcerpt('![diagram](https://example.com/a.png)\n\n---')).toBe('');
  });

  it('keeps plain text as is', () => {
    expect(markdownExcerpt('Ship the beta to early customers.')).toBe(
      'Ship the beta to early customers.'
    );
  });

  it('strips headings, emphasis, code, and strikethrough', () => {
    expect(markdownExcerpt('## Goal: **ship** the _beta_ with `ws` and ~~no~~ *few* bugs')).toBe(
      'Goal: ship the beta with ws and no few bugs'
    );
  });

  it('turns links into their text and drops images and HTML tags', () => {
    expect(
      markdownExcerpt(
        'See [the spec](https://example.com/spec) ![logo](logo.png) and <b>notes</b> at <https://example.com>.'
      )
    ).toBe('See the spec and notes at https://example.com.');
  });

  it('strips list markers, task boxes, and blockquotes', () => {
    expect(markdownExcerpt('- first\n- [x] second\n1. third')).toBe('first second third');
    expect(markdownExcerpt('> quoted\n> text')).toBe('quoted text');
  });

  it('uses the first paragraph with text', () => {
    expect(markdownExcerpt('# Outcome\n\nCustomers can plan releases.\n\nSecond paragraph.')).toBe(
      'Outcome'
    );
    expect(
      markdownExcerpt(
        '![cover](cover.png)\n\n```js\nconst x = 1;\n```\n\nFirst real text.\n\nMore.'
      )
    ).toBe('First real text.');
  });

  it('collapses whitespace and line breaks inside the paragraph', () => {
    expect(markdownExcerpt('One line\nnext   line\twith tab')).toBe('One line next line with tab');
  });

  it('keeps snake_case words and escaped characters', () => {
    expect(markdownExcerpt('Rename snake_case_name to \\*literal\\* \\_text\\_')).toBe(
      'Rename snake_case_name to *literal* _text_'
    );
  });

  it('decodes HTML entities that Markdown serializers emit', () => {
    expect(markdownExcerpt('Tom &amp; Jerry&#x20;&lt;3')).toBe('Tom & Jerry <3');
  });

  it('truncates long text on a word boundary with an ellipsis', () => {
    const words = Array.from({ length: 60 }, (_, i) => `word${i}`).join(' ');
    const excerpt = markdownExcerpt(words);
    expect(excerpt.endsWith('…')).toBe(true);
    expect(excerpt.length).toBeLessThanOrEqual(151);
    expect(excerpt.length).toBeGreaterThan(100);
    expect(words.startsWith(excerpt.slice(0, -1))).toBe(true);
    expect(excerpt.slice(0, -1)).toMatch(/word\d+$/);
  });

  it('cuts a single long word at the limit', () => {
    const excerpt = markdownExcerpt('x'.repeat(400), 20);
    expect(excerpt).toBe(`${'x'.repeat(20)}…`);
  });
});
