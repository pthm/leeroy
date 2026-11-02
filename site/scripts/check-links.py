# Checks the built site's links: every href and src resolved against its page (base /leeroy) and found in dist/, and
# every #anchor found on its page. External links are printed, not fetched.
#   npm run build && python3 scripts/check-links.py dist
import os, re, sys, html
from urllib.parse import urljoin, urlparse, unquote
dist, base = sys.argv[1], '/leeroy'
pages = {}
for root, _, files in os.walk(dist):
    for f in files:
        if f.endswith('.html'):
            p = os.path.join(root, f)
            rel = '/' + os.path.relpath(p, dist)
            url = base + (rel[:-len('index.html')] if rel.endswith('index.html') else rel)
            pages[url] = open(p, encoding='utf-8').read()
def exists(path):
    if not path.startswith(base): return False
    fs = os.path.join(dist, unquote(path[len(base):]).lstrip('/'))
    return os.path.isfile(fs) or os.path.isfile(os.path.join(fs, 'index.html'))
bad, ext, anchors = [], set(), []
for url, doc in pages.items():
    ids = set(re.findall(r'\bid="([^"]+)"', doc))
    for attr, link in re.findall(r'\b(href|src|data-full)="([^"]+)"', doc):
        link = html.unescape(link)
        if link.startswith(('mailto:', 'data:', 'javascript:')): continue
        u = urlparse(urljoin('http://x' + url, link))
        if u.netloc != 'x':
            ext.add(link); continue
        if not exists(u.path): bad.append((url, link))
        if u.fragment and u.path.rstrip('/') == url.rstrip('/') and u.fragment not in ids: anchors.append((url, link))
        elif u.fragment and u.path.rstrip('/') != url.rstrip('/'):
            target = pages.get(u.path if u.path.endswith('/') else u.path + '/') or pages.get(u.path)
            if target is not None and f'id="{u.fragment}"' not in target: anchors.append((url, link))
print(len(pages), 'pages')
print('BROKEN', len(bad)); [print(' ', a, '->', b) for a, b in bad]
print('BAD ANCHORS', len(anchors)); [print(' ', a, '->', b) for a, b in anchors]
print('external', len(ext)); [print(' ', e) for e in sorted(ext) if 'pthm.github.io' not in e]
sys.exit(1 if bad or anchors else 0)
