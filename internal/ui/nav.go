package ui

// Nav is the top-of-page ticker shared by /docs/, /api/, /server/,
// and placeholder pages.  (The index page has its own sidebar nav.)
//
// The template expects `.RepoName` in the data passed to Execute.
const Nav = `<div class="ticker">
  <span><b>{{.RepoName}}</b></span><span class="dot">·</span>
  <span><a href="/">Home</a></span><span class="dot">·</span>
  <span><a href="/source/">Source</a></span><span class="dot">·</span>
  <span><a href="/docs/">Docs</a></span><span class="dot">·</span>
  <span><a href="/api/">API</a></span><span class="dot">·</span>
  <span><a href="/server/">Server</a></span>
</div>`
