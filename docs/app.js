// FindIt product page interactions. Plain JS, no dependencies.
(() => {
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const REPO = 'manju4682/findit';

  // Nav gets a border once the page scrolls.
  const nav = document.querySelector('.nav');
  const onScroll = () => nav.classList.toggle('is-scrolled', window.scrollY > 8);
  window.addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // Reveal-on-scroll.
  const revealer = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          e.target.classList.add('is-visible');
          revealer.unobserve(e.target);
        }
      }
    },
    { threshold: 0.15, rootMargin: '0px 0px -40px 0px' },
  );
  document.querySelectorAll('.reveal').forEach((el) => revealer.observe(el));

  // Hero window follows the pointer a little.
  const tilt = document.querySelector('[data-tilt]');
  if (tilt && !reduceMotion && window.matchMedia('(pointer: fine)').matches) {
    let frame = 0;
    document.querySelector('.hero').addEventListener('pointermove', (ev) => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const x = ev.clientX / window.innerWidth - 0.5;
        const y = ev.clientY / window.innerHeight - 0.5;
        tilt.style.transform = `rotateY(${-6 + x * 8}deg) rotateX(${3 - y * 6}deg)`;
      });
    });
  }

  // Spotlight that follows the pointer on feature tiles.
  document.querySelectorAll('.tile').forEach((tile) => {
    tile.addEventListener('pointermove', (ev) => {
      const r = tile.getBoundingClientRect();
      tile.style.setProperty('--mx', `${ev.clientX - r.left}px`);
      tile.style.setProperty('--my', `${ev.clientY - r.top}px`);
    });
  });

  // ---- Diagnosis demo: a drive map is scanned cell by cell, then explained. ----
  const scan = document.querySelector('[data-scan]');
  if (scan) {
    const disk = scan.querySelector('[data-disk]');
    const bar = scan.querySelector('[data-scan-bar]');
    const diag = scan.querySelector('[data-diag]');
    const typed = scan.querySelector('[data-typed]');
    const sources = [...scan.querySelectorAll('[data-sources] li')];
    const count = (k) => scan.querySelector(`[data-count="${k}"]`);
    const NARRATIVE =
      'This drive has an exFAT filesystem. We also found signs of an earlier FAT32 filesystem underneath — your older files may still be recoverable. We can also see about 412 photos and 23 videos by content.';
    const FINAL = { photo: 412, video: 23 };

    // A plausible drive layout: today's filesystem at the start, the old one's
    // tables just after, then runs of photos, videos and free space.
    let seed = 42;
    const rnd = () => (seed = (seed * 16807) % 2147483647) / 2147483647;
    const N = 32 * 12;
    const layout = [];
    while (layout.length < 10) layout.push('current');
    while (layout.length < 22) layout.push('old');
    while (layout.length < N) {
      const r = rnd();
      const type = r < 0.42 ? 'photo' : r < 0.72 ? 'empty' : r < 0.84 ? 'video' : r < 0.93 ? 'old' : 'empty';
      const run = 2 + Math.floor(rnd() * (type === 'video' ? 8 : 10));
      for (let i = 0; i < run && layout.length < N; i++) layout.push(type);
    }
    const totals = { photo: 0, video: 0 };
    layout.forEach((t) => t in totals && totals[t]++);

    const cells = layout.map(() => disk.appendChild(document.createElement('i')));
    let raf = 0;
    let timers = [];

    const reset = () => {
      cancelAnimationFrame(raf);
      timers.forEach(clearTimeout);
      timers = [];
      cells.forEach((c) => (c.className = ''));
      bar.style.width = '0%';
      count('old').textContent = '—';
      count('photo').textContent = '0';
      count('video').textContent = '0';
      diag.classList.remove('is-on');
      typed.textContent = '';
      typed.classList.remove('is-done');
      sources.forEach((li) => li.classList.remove('is-on'));
    };

    const explain = () => {
      diag.classList.add('is-on');
      if (reduceMotion) {
        typed.textContent = NARRATIVE;
        typed.classList.add('is-done');
        sources.forEach((li) => li.classList.add('is-on'));
        return;
      }
      let i = 0;
      const tick = () => {
        typed.textContent = NARRATIVE.slice(0, ++i);
        if (i < NARRATIVE.length) {
          timers.push(setTimeout(tick, 14));
        } else {
          typed.classList.add('is-done');
          sources.forEach((li, k) => timers.push(setTimeout(() => li.classList.add('is-on'), 200 + k * 220)));
        }
      };
      tick();
    };

    const run = () => {
      reset();
      const seen = { photo: 0, video: 0, old: 0 };
      const paint = (upto) => {
        for (let i = 0; i < upto; i++) {
          if (cells[i].className && !cells[i].classList.contains('is-scan')) continue;
          cells[i].className = layout[i];
          if (layout[i] in seen) seen[layout[i]]++;
        }
        if (upto < N) cells[upto].className = 'is-scan';
        bar.style.width = `${(upto / N) * 100}%`;
        count('photo').textContent = Math.round((seen.photo / totals.photo) * FINAL.photo);
        count('video').textContent = Math.round((seen.video / totals.video) * FINAL.video);
        count('old').textContent = seen.old ? 'found' : '—';
      };
      if (reduceMotion) {
        paint(N);
        explain();
        return;
      }
      const DURATION = 3200;
      let start = 0;
      let done = 0;
      const step = (t) => {
        start ||= t;
        const upto = Math.min(N, Math.floor(((t - start) / DURATION) * N));
        if (upto > done) {
          paint(upto);
          done = upto;
        }
        if (upto < N) raf = requestAnimationFrame(step);
        else explain();
      };
      raf = requestAnimationFrame(step);
    };

    reset();
    new IntersectionObserver(
      (entries, obs) => {
        if (entries[0].isIntersecting) {
          obs.disconnect();
          run();
        }
      },
      { threshold: 0.4 },
    ).observe(scan);
    scan.querySelector('[data-scan-replay]').addEventListener('click', run);
  }

  // ---- How it works: steps auto-advance while visible; click to jump. ----
  const steps = document.querySelector('[data-steps]');
  if (steps) {
    const tabs = [...steps.querySelectorAll('[data-step]')];
    const shots = [...steps.querySelectorAll('.steps__stage img')];
    let active = 0;
    const show = (i) => {
      active = i;
      tabs.forEach((t, k) => t.setAttribute('aria-selected', String(k === i)));
      shots.forEach((s, k) => s.classList.toggle('is-active', k === i));
    };
    tabs.forEach((t, i) =>
      t.addEventListener('click', () => {
        // Re-selecting restarts the progress bar.
        t.setAttribute('aria-selected', 'false');
        void t.offsetWidth;
        show(i);
      }),
    );
    steps.addEventListener('animationend', (e) => {
      if (e.target.classList.contains('steps__progress')) show((active + 1) % tabs.length);
    });
    steps.addEventListener('pointerenter', () => steps.classList.add('is-paused'));
    steps.addEventListener('pointerleave', () => steps.classList.remove('is-paused'));
    new IntersectionObserver((entries) => steps.classList.toggle('is-paused', !entries[0].isIntersecting), {
      threshold: 0.3,
    }).observe(steps);
  }

  // ---- Screenshot gallery tabs. ----
  const gallery = document.querySelector('[data-gallery]');
  if (gallery) {
    const captions = [
      'Browse the old folders exactly as they were, with a thumbnail for every photo.',
      'Details view with a large preview and a clear status for every file.',
      'Even with no filesystem left, photos are found by their content and checked before they’re listed.',
    ];
    const tabs = [...gallery.querySelectorAll('[data-shot]')];
    const shots = [...gallery.querySelectorAll('.gallery__stage img')];
    const caption = gallery.querySelector('[data-caption]');
    tabs.forEach((t, i) =>
      t.addEventListener('click', () => {
        tabs.forEach((x, k) => x.setAttribute('aria-selected', String(k === i)));
        shots.forEach((s, k) => s.classList.toggle('is-active', k === i));
        caption.textContent = captions[i];
      }),
    );
  }

  // ---- Latest release details (best effort; the page works without them). ----
  const applyRelease = (rel) => {
    if (!rel || !rel.tag_name) return;
    const pill = document.querySelector('[data-version]');
    if (pill) {
      pill.textContent = rel.tag_name;
      pill.hidden = false;
    }
    const suffix = document.querySelector('[data-version-suffix]');
    if (suffix) suffix.textContent = ` ${rel.tag_name}`;
    const meta = document.querySelector('[data-release-meta]');
    if (meta && rel.published_at) {
      const date = new Date(rel.published_at).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
      meta.textContent = `${rel.tag_name} · released ${date} · macOS 12 or later · Apple Silicon (M1 and newer)`;
    }
  };
  const cached = sessionStorage.getItem('findit-release');
  if (cached) {
    applyRelease(JSON.parse(cached));
  } else {
    fetch(`https://api.github.com/repos/${REPO}/releases/latest`, { headers: { Accept: 'application/vnd.github+json' } })
      .then((r) => (r.ok ? r.json() : null))
      .then((rel) => {
        if (!rel) return;
        const slim = { tag_name: rel.tag_name, published_at: rel.published_at };
        sessionStorage.setItem('findit-release', JSON.stringify(slim));
        applyRelease(slim);
      })
      .catch(() => {});
  }
})();
