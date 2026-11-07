package swarm

import (
	"bytes"
	"html/template"
	"path/filepath"

	"github.com/Novirmor/homonto/internal/fsutil"
)

var page = template.Must(template.New("compare").Funcs(template.FuncMap{
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Swarm {{.Swarm}}</title>
<style>
:root { color-scheme: light dark; --fg:#1d1d1f; --muted:#6e6e73; --bg:#fff; --line:#d2d2d7; --ok:#1a7f37; --bad:#cf222e; }
@media (prefers-color-scheme: dark) { :root { --fg:#f5f5f7; --muted:#a1a1a6; --bg:#161618; --line:#3a3a3c; --ok:#3fb950; --bad:#f85149; } }
body { margin:0; padding:16px; font:14px/1.45 system-ui, sans-serif; color:var(--fg); background:var(--bg); }
h1 { font-size:18px; margin:0 0 4px; } p.meta { color:var(--muted); margin:0 0 16px; }
.grid { display:grid; grid-template-columns:repeat({{len .Candidates}}, minmax(0,1fr)); gap:12px; }
.col { border:1px solid var(--line); border-radius:8px; padding:12px; min-width:0; }
.col.selected { outline:2px solid var(--ok); }
h2 { font-size:16px; margin:0 0 8px; }
dl { display:grid; grid-template-columns:auto 1fr; gap:2px 8px; margin:0 0 8px; } dt { color:var(--muted); } dd { margin:0; overflow-wrap:anywhere; }
.ok { color:var(--ok); } .bad { color:var(--bad); }
iframe { width:100%; height:480px; border:1px solid var(--line); border-radius:6px; background:#fff; }
ul { margin:4px 0 8px; padding-left:18px; }
@media (max-width: 720px) { .grid { grid-template-columns:1fr; } }
</style>
</head>
<body>
<h1>Swarm {{.Swarm}}</h1>
<p class="meta">Engine {{.Engine}} · brief {{short .Brief.SHA256}} · checks: {{if .Checks}}{{range $i, $c := .Checks}}{{if $i}}, {{end}}{{$c}}{{end}}{{else}}none configured{{end}}{{if .Selected}} · selected: <strong>{{.Selected}}</strong>{{end}}</p>
<div class="grid">
{{range .Candidates}}{{$cand := .}}<section class="col{{if .Selected}} selected{{end}}">
<h2>{{.Name}} {{if .Verified}}<span class="ok">verified</span>{{else}}<span class="bad">not verified</span>{{end}}</h2>
<dl>
<dt>agent</dt><dd>{{.Agent}}</dd>
{{range $h, $m := .Models}}<dt>{{$h}}</dt><dd>{{$m}}</dd>{{end}}
{{range $alias, $d := .Diff}}<dt>{{$alias}}</dt><dd>{{short (index $.Bases $alias)}}..{{short (index $cand.Revisions $alias)}} · {{$d.Commits}} commits · {{$d.Files}} files +{{$d.Insertions}} −{{$d.Deletions}}</dd>{{end}}
{{range $name, $c := .Checks}}<dt>{{$name}}</dt><dd class="{{if and (eq $c.Status "passed") (not $c.Stale)}}ok{{else}}bad{{end}}">{{$c.Status}}{{if $c.Stale}} (stale){{end}} · exit {{$c.ExitCode}} · {{$c.Duration}}</dd>{{end}}
</dl>
{{if .Unverified}}<ul>{{range .Unverified}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{range .Previews}}{{if .URL}}<p><a href="{{.URL}}" target="_blank" rel="noopener">{{.Preview}} preview</a> · until {{.Deadline.Format "15:04"}}</p>
<iframe src="{{.URL}}" title="{{.Candidate}} {{.Preview}} preview" sandbox="allow-scripts allow-forms allow-same-origin"></iframe>{{else}}<p class="bad">{{.Preview}} preview {{.State}}</p>{{end}}{{end}}
</section>
{{end}}</div>
</body>
</html>
`))

// WriteHTML renders the side-by-side page into the swarm directory and
// returns its path. Running previews are embedded next to their evidence.
func (s *Store) WriteHTML(cmp *Comparison) (string, error) {
	dir, err := s.dir(cmp.Swarm)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := page.Execute(&buf, cmp); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "compare.html")
	return path, fsutil.WriteControlPlane(path, buf.Bytes(), 0o600)
}
