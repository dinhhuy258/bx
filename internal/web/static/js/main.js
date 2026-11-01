import { showProjects } from './projects.js';
import { showProject } from './project.js';
import { showSession } from './session.js';
import { showSettings } from './settings.js';

// Hash routes: #/  #/p/{name}  #/p/{name}/s/{num}  #/settings. Each screen returns {stop()}.
const root = document.getElementById('app');
let current = null;

function route() {
  if (current) current.stop();
  root.replaceChildren();
  const parts = location.hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent);
  if (parts[0] === 'p' && parts[1] && parts[2] === 's' && parts[3]) current = showSession(root, parts[1], Number(parts[3]));
  else if (parts[0] === 'p' && parts[1]) current = showProject(root, parts[1]);
  else if (parts[0] === 'settings') current = showSettings(root);
  else current = showProjects(root);
  window.scrollTo(0, 0);
}

window.addEventListener('hashchange', route);
route();
