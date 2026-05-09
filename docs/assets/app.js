const sidebar = document.querySelector('#sidebar');
const navToggle = document.querySelector('.nav-toggle');
const navLinks = Array.from(document.querySelectorAll('.sidebar nav a'));
const sections = Array.from(document.querySelectorAll('.doc-section'));
const search = document.querySelector('#docs-search');

if (navToggle && sidebar) {
  navToggle.addEventListener('click', () => {
    const open = sidebar.classList.toggle('open');
    navToggle.setAttribute('aria-expanded', String(open));
  });
}

navLinks.forEach((link) => {
  link.addEventListener('click', () => {
    sidebar?.classList.remove('open');
    navToggle?.setAttribute('aria-expanded', 'false');
  });
});

const observer = new IntersectionObserver((entries) => {
  const visible = entries
    .filter((entry) => entry.isIntersecting)
    .sort((a, b) => b.intersectionRatio - a.intersectionRatio)[0];
  if (!visible) return;
  const id = visible.target.getAttribute('id');
  navLinks.forEach((link) => {
    link.classList.toggle('active', link.getAttribute('href') === `#${id}`);
  });
}, { rootMargin: '-110px 0px -65% 0px', threshold: [0.1, 0.25, 0.5] });

sections.forEach((section) => observer.observe(section));

document.querySelectorAll('pre').forEach((pre) => {
  const code = pre.querySelector('code');
  if (!code) return;
  const button = document.createElement('button');
  button.className = 'copy-button';
  button.type = 'button';
  button.textContent = 'Copy';
  button.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(code.innerText);
      button.textContent = 'Copied';
      setTimeout(() => { button.textContent = 'Copy'; }, 1500);
    } catch {
      button.textContent = 'Select';
      setTimeout(() => { button.textContent = 'Copy'; }, 1500);
    }
  });
  pre.append(button);
});

function normalize(value) {
  return value.toLowerCase().replace(/\s+/g, ' ').trim();
}

function applySearch(query) {
  const term = normalize(query);
  sections.forEach((section) => {
    const haystack = normalize(`${section.dataset.title || ''} ${section.textContent || ''}`);
    section.classList.toggle('hidden-by-search', Boolean(term) && !haystack.includes(term));
  });
}

search?.addEventListener('input', (event) => {
  applySearch(event.target.value);
});
