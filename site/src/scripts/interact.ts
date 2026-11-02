// Interactivity for the results pages' charts and maps, one script for all of them:
// - tooltips: any <title> or title="" inside a [data-chart] becomes a styled tooltip that follows the pointer;
// - legends: a [data-toggle=key] button hides or shows the [data-key=key] elements of its chart; hovering either
//   highlights that key and dims the rest;
// - enlarge: a [data-enlarge] button opens its figure full-screen; maps ([data-zoom]) zoom with the wheel or pinch and
//   pan by dragging there, double-click resets;
// - crosshair: an svg with [data-series] shows each line's value at the pointer's time.

const tip = document.createElement('div');
tip.className = 'chart-tip';
tip.hidden = true;
document.body.append(tip);

function adoptTitles(root: ParentNode) {
  for (const t of root.querySelectorAll('[data-chart] title')) {
    t.parentElement?.setAttribute('data-tip', t.textContent ?? '');
    t.remove();
  }
  for (const e of root.querySelectorAll<HTMLElement>('[data-chart] [title]')) {
    e.dataset.tip = e.title;
    e.removeAttribute('title');
  }
}

function showTip(html: string, x: number, y: number) {
  tip.innerHTML = html;
  tip.hidden = false;
  const r = tip.getBoundingClientRect();
  tip.style.left = `${Math.min(x + 14, innerWidth - r.width - 8)}px`;
  tip.style.top = `${y + 16 + r.height > innerHeight ? y - r.height - 10 : y + 16}px`;
}

const esc = (s: string) => s.replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]!);

document.addEventListener('pointermove', e => {
  const el = (e.target as Element).closest?.('[data-tip]');
  if (el && !(e.target as Element).closest('[data-series]')) showTip(esc((el as HTMLElement).dataset.tip!), e.clientX, e.clientY);
  else if (!(e.target as Element).closest?.('[data-series]')) tip.hidden = true;
});

// legends and highlight
function chartOf(el: Element) {
  return el.closest('[data-chart]');
}
document.addEventListener('click', e => {
  const b = (e.target as Element).closest?.('[data-toggle]') as HTMLElement | null;
  if (!b) return;
  const chart = chartOf(b)!;
  const off = b.getAttribute('aria-pressed') !== 'false';
  b.setAttribute('aria-pressed', String(!off));
  for (const el of chart.querySelectorAll(`[data-key="${CSS.escape(b.dataset.toggle!)}"]`)) el.classList.toggle('is-off', off);
});
document.addEventListener('pointerover', e => {
  const el = (e.target as Element).closest?.('[data-key],[data-toggle]') as HTMLElement | null;
  const chart = el && chartOf(el);
  if (!el || !chart) return;
  const key = el.dataset.key ?? el.dataset.toggle!;
  chart.classList.add('is-hl');
  for (const k of chart.querySelectorAll('[data-key]')) k.classList.toggle('is-on', (k as HTMLElement).dataset.key === key);
});
document.addEventListener('pointerout', e => {
  const el = (e.target as Element).closest?.('[data-key],[data-toggle]');
  const chart = el && chartOf(el);
  if (chart && !chart.contains(e.relatedTarget as Node)) chart.classList.remove('is-hl');
  else if (chart && !(e.relatedTarget as Element)?.closest?.('[data-key],[data-toggle]')) chart.classList.remove('is-hl');
});

// enlarge
const dialog = document.createElement('dialog');
dialog.className = 'enlarge';
document.body.append(dialog);
dialog.addEventListener('click', e => {
  if (e.target === dialog || (e.target as Element).closest('[data-close]')) dialog.close();
});
dialog.addEventListener('close', () => {
  dialog.replaceChildren();
  tip.hidden = true;
});
document.addEventListener('keydown', e => {
  if ((e.key === 'Enter' || e.key === ' ') && (e.target as Element).matches?.('[data-enlarge][role=button]')) {
    e.preventDefault();
    (e.target as HTMLElement).click();
  }
});
document.addEventListener('click', e => {
  const b = (e.target as Element).closest?.('[data-enlarge]');
  if (!b || dialog.open) return;
  const fig = b.closest('figure')!.cloneNode(true) as HTMLElement;
  for (const el of fig.querySelectorAll('[data-enlarge]')) {
    if (el.tagName === 'BUTTON') el.remove(); // an Enlarge button
    else el.removeAttribute('data-enlarge'); // the map itself, clicked: stays, and zooms instead
  }
  const close = document.createElement('button');
  close.type = 'button';
  close.dataset.close = '';
  close.className = 'enlarge-close';
  close.setAttribute('aria-label', 'Close');
  close.textContent = '✕';
  dialog.replaceChildren(close, fig);
  for (const im of fig.querySelectorAll('image[data-full]')) im.setAttribute('href', im.getAttribute('data-full')!); // the sharp one
  dialog.showModal();
  for (const svg of fig.querySelectorAll<SVGSVGElement>('svg[data-zoom]')) {
    zoomable(svg);
    const hint = document.createElement('p');
    hint.className = 'mt-2 font-mono text-[11px] text-faint';
    hint.textContent = 'Scroll to zoom · drag to pan · double-click to reset · click a name to hide it';
    svg.closest('div')!.after(hint);
  }
  for (const svg of fig.querySelectorAll<SVGSVGElement>('svg[data-series]')) crosshair(svg);
});

// wheel and drag on an enlarged map
function zoomable(svg: SVGSVGElement) {
  const base = svg.viewBox.baseVal;
  const home = [base.x, base.y, base.width, base.height];
  const set = (x: number, y: number, w: number, h: number) => svg.setAttribute('viewBox', `${x} ${y} ${w} ${h}`);
  const at = (e: { clientX: number; clientY: number }) => {
    const r = svg.getBoundingClientRect();
    const v = svg.viewBox.baseVal;
    return [v.x + ((e.clientX - r.left) / r.width) * v.width, v.y + ((e.clientY - r.top) / r.height) * v.height];
  };
  svg.style.cursor = 'grab';
  svg.addEventListener('wheel', e => {
    e.preventDefault();
    const v = svg.viewBox.baseVal;
    const k = Math.min(Math.max(e.deltaY > 0 ? 1.15 : 1 / 1.15, home[2] / 40 / v.width), home[2] / v.width);
    const [px, py] = at(e);
    set(px - (px - v.x) * k, py - (py - v.y) * k, v.width * k, v.height * k);
  }, { passive: false });
  let drag: { x: number; y: number; vx: number; vy: number } | null = null;
  svg.addEventListener('pointerdown', e => {
    const v = svg.viewBox.baseVal;
    drag = { x: e.clientX, y: e.clientY, vx: v.x, vy: v.y };
    svg.setPointerCapture(e.pointerId);
    svg.style.cursor = 'grabbing';
  });
  svg.addEventListener('pointermove', e => {
    if (!drag) return;
    const r = svg.getBoundingClientRect();
    const v = svg.viewBox.baseVal;
    set(drag.vx - ((e.clientX - drag.x) / r.width) * v.width, drag.vy - ((e.clientY - drag.y) / r.height) * v.height, v.width, v.height);
  });
  svg.addEventListener('pointerup', () => {
    drag = null;
    svg.style.cursor = 'grab';
  });
  svg.addEventListener('dblclick', () => set(...(home as [number, number, number, number])));
}

// crosshair: data-series = {x0, x1, t1, y: (level) => px, lines: [{name, cls, pts: [[t, v]...]}], fmt}
function crosshair(svg: SVGSVGElement) {
  const d = JSON.parse(svg.dataset.series!) as {
    x0: number; x1: number; t1: number; top: number; bottom: number;
    lines: { name: string; cls: string; pts: [number, number][] }[];
  };
  const ns = 'http://www.w3.org/2000/svg';
  const line = document.createElementNS(ns, 'line');
  line.setAttribute('stroke', 'var(--color-muted)');
  line.setAttribute('stroke-dasharray', '3 3');
  line.setAttribute('y1', String(d.top));
  line.setAttribute('y2', String(d.bottom));
  line.style.display = 'none';
  svg.append(line);
  const hours = (s: number) => (s < 3600 ? `${Math.round(s / 60)} min` : `${Math.floor(s / 3600)} h ${String(Math.round((s % 3600) / 60)).padStart(2, '0')}`);
  svg.addEventListener('pointermove', e => {
    const own = (e.target as Element).closest('[data-tip]') as HTMLElement | null; // a death tick, a wipe band
    const r = svg.getBoundingClientRect();
    const v = svg.viewBox.baseVal;
    const x = v.x + ((e.clientX - r.left) / r.width) * v.width;
    if (own && own.tagName !== 'rect') {
      line.style.display = 'none';
      showTip(esc(own.dataset.tip!), e.clientX, e.clientY);
      return;
    }
    if (x < d.x0 || x > d.x1) {
      line.style.display = 'none';
      tip.hidden = true;
      return;
    }
    const t = ((x - d.x0) / (d.x1 - d.x0)) * d.t1;
    line.setAttribute('x1', String(x));
    line.setAttribute('x2', String(x));
    line.style.display = '';
    const rows = d.lines
      .map(l => {
        let best = l.pts[0];
        for (const p of l.pts) if (p[0] <= t) best = p;
        return `<span class="${l.cls}">●</span> ${esc(l.name)} <b>${best[1].toFixed(1)}</b>`;
      })
      .join('<br>');
    const band = own ? `<br><span class="chart-tip-head">${esc(own.dataset.tip!)}</span>` : '';
    showTip(`<span class="chart-tip-head">${hours(t)}</span><br>${rows}${band}`, e.clientX, e.clientY);
  });
  svg.addEventListener('pointerleave', () => {
    line.style.display = 'none';
    tip.hidden = true;
  });
}

adoptTitles(document);
for (const svg of document.querySelectorAll<SVGSVGElement>('svg[data-series]')) crosshair(svg);
