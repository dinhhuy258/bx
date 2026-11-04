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
  // body is an api.Action; a 409 whose .result.exists is set means the branch exists
  newStory: (p, body) => call('POST', `/api/projects/${enc(p)}/stories`, body),
  storyInfo: (p) => call('GET', `/api/projects/${enc(p)}/storyinfo`),
  // fetch: run git fetch first (slow on a big repo)
  branches: (p, fetch) => call('GET', `/api/projects/${enc(p)}/branches${fetch ? '?fetch=1' : ''}`),
  // q: '' for the open pull requests, else a search, #123 or a pull request URL
  prs: (p, q) => call('GET', `/api/projects/${enc(p)}/prs?q=${enc(q || '')}`),
  action: (p, action, body) => call('POST', `/api/projects/${enc(p)}/${action}`, body),
  settings: () => call('GET', '/api/settings'),
  saveSettings: (values) => call('PUT', '/api/settings', { values }),
  // content is base64; the reply's .path is where the server saved the file
  upload: (p, num, name, content) =>
    call('POST', `/api/projects/${enc(p)}/sessions/${num}/files`, { name, content }),
  // the session's HTML pages, newest first: [{ name, size, modified, url }]
  artifacts: (p, num) => call('GET', `/api/projects/${enc(p)}/sessions/${num}/artifacts`),
  // what can follow / in the session's prompt, by name: [{ name, description, source }]
  slashCommands: (p, num) => call('GET', `/api/projects/${enc(p)}/sessions/${num}/slash-commands`),
  streamURL: (p, num, cols, rows) =>
    `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/projects/${enc(p)}/sessions/${num}/stream?cols=${cols}&rows=${rows}`,
  // shells: story '' is Chats (the project folder); the reply of newShell has the new .k
  newShell: (p, story) => call('POST', `/api/projects/${enc(p)}/shells`, { story }),
  closeShell: (p, story, k) => call('POST', `/api/projects/${enc(p)}/shells/close`, { story, k }),
  // an empty title goes back to showing the command
  renameShell: (p, story, k, title) => call('POST', `/api/projects/${enc(p)}/shells/rename`, { story, k, title }),
  // the story's diff with its review comments; saveReview sends {version, comments} back whole
  review: (p, s) => call('GET', `/api/projects/${enc(p)}/stories/${enc(s)}/review`),
  saveReview: (p, s, body) => call('PUT', `/api/projects/${enc(p)}/stories/${enc(s)}/review`, body),
  // body is {session: n} or {new: true}; the reply's .message says what happened
  sendReview: (p, s, body) => call('POST', `/api/projects/${enc(p)}/stories/${enc(s)}/review/send`, body),
  clearSent: (p, s) => call('POST', `/api/projects/${enc(p)}/stories/${enc(s)}/review/clear-sent`, {}),
  // ask Claude about diff lines (the review comes back with it running), or stop / dismiss one
  explain: (p, s, body) => call('POST', `/api/projects/${enc(p)}/stories/${enc(s)}/review/explain`, body),
  deleteExplain: (p, s, id) => call('DELETE', `/api/projects/${enc(p)}/stories/${enc(s)}/review/explain/${enc(id)}`),
  shellStreamURL: (p, story, k, cols, rows) =>
    `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/projects/${enc(p)}/shells/stream?story=${enc(story)}&k=${k}&cols=${cols}&rows=${rows}`,
};
