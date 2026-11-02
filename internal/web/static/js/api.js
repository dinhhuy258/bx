// Wrappers for bx serve's JSON API. A non-2xx reply throws an Error with .status and .result
// (the api.Result body, e.g. dirty files on a 409).
async function call(method, path, body) {
  const opt = { method, headers: {} };
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  const r = await fetch(path, opt);
  let data = null;
  try { data = await r.json(); } catch { /* empty body */ }
  if (!r.ok) {
    const e = new Error((data && data.error) || `${r.status} ${r.statusText}`);
    e.status = r.status;
    e.result = data;
    throw e;
  }
  return data;
}

const enc = encodeURIComponent;

export const api = {
  projects: () => call('GET', '/api/projects'),
  snapshot: (p) => call('GET', `/api/projects/${enc(p)}`),
  createProject: (body) => call('POST', '/api/projects', body),
  githubRepos: () => call('GET', '/api/github/repos'),
  projectRepos: (p) => call('GET', `/api/projects/${enc(p)}/repos`),
  action: (p, action, body) => call('POST', `/api/projects/${enc(p)}/${action}`, body),
  settings: () => call('GET', '/api/settings'),
  saveSettings: (values) => call('PUT', '/api/settings', { values }),
  // content is base64; the reply's .path is where the server saved the file
  upload: (p, num, name, content) =>
    call('POST', `/api/projects/${enc(p)}/sessions/${num}/files`, { name, content }),
  streamURL: (p, num, cols, rows) =>
    `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/projects/${enc(p)}/sessions/${num}/stream?cols=${cols}&rows=${rows}`,
};
