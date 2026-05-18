// Package server constructs the chi-based HTTP handler. It is wrapped by
// internal/httproutes into the SDK's HttpRoutes.v1 RPC.
package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/request"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/runtime"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/store"
)

type Deps struct {
	EnableAutoMonitoring bool
	BookwarehouseClient  *bookwarehouse.Client
	Store                *store.Store
	Config               runtime.Config
}

type Server struct {
	deps Deps
}

func New(d Deps) *Server { return &Server{deps: d} }

// chiPathValueShim copies chi.URLParam values into the request via
// SetPathValue, so handlers using stdlib r.PathValue work under chi.
func chiPathValueShim(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			for i, k := range rctx.URLParams.Keys {
				if i < len(rctx.URLParams.Values) {
					r.SetPathValue(k, rctx.URLParams.Values[i])
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/admin", s.handleAdminHome)
	r.Get("/admin/", s.handleAdminHome)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/capabilities", s.handleCapabilities)
		r.Get("/admin/diagnostics", s.handleDiagnostics)
		r.Get("/admin/test-search", s.handleTestSearch)
		if s.deps.BookwarehouseClient != nil {
			ch := catalog.NewHandler(s.deps.BookwarehouseClient)
			ch.Mount(r)
			rh := request.NewHandler(s.deps.BookwarehouseClient)
			r.Get("/requests/{external_id}", chiPathValueShim(rh.Snapshot()).ServeHTTP)
		}
	})
	return r
}

func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en" data-theme="` + adminTheme(r) + `">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>BookWarehouse Ebook</title><style>` + adminThemeCSS() + `</style></head>
<body>
<main class="shell">
<a class="back" href="/admin/plugins">&larr; Plugins</a>
<header><p class="eyebrow">Ebook backend</p><h1>BookWarehouse Ebook</h1><p>Calibre-backed catalog, file delivery, external search, and request monitoring.</p></header>
<nav class="tabs" aria-label="BookWarehouse Ebook admin sections">
<button class="tab active" data-tab-target="readiness" type="button">Readiness</button>
<button class="tab" data-tab-target="browser" type="button">Browser</button>
<button class="tab" data-tab-target="request-preview" type="button">Request preview</button>
<button class="tab" data-tab-target="reconcile" type="button">Reconcile</button>
</nav>
<section class="tab-panel active" id="readiness">
<article class="panel"><div class="panel-head"><div><h2>Setup status</h2><p class="muted">Confirms database, upstream API, auto-monitoring, and current request health.</p></div><span id="ready-badge" class="badge">Loading</span></div><div id="status" class="cards muted">Loading diagnostics...</div></article>
</section>
<section class="tab-panel" id="browser">
<article class="panel"><div class="panel-head"><div><h2>Backend browser</h2><p class="muted">Search BookWarehouse before changing portal routing or request policy.</p></div></div><form id="search-form" class="row"><input id="q" value="foundation" placeholder="Search title or author" aria-label="Search query"><button type="submit">Test search</button></form><pre id="search-output" class="output">No test run yet.</pre></article>
</section>
<section class="tab-panel" id="request-preview">
<article class="panel"><div class="panel-head"><div><h2>Request preview</h2><p class="muted">Build the expected upstream payload before enabling request forwarding for users.</p></div></div><form id="request-preview-form" class="preview-grid"><input id="preview-title" placeholder="Title" aria-label="Title"><input id="preview-authors" placeholder="Authors, comma separated" aria-label="Authors"><input id="preview-isbn" placeholder="ISBN" aria-label="ISBN"><input id="preview-format" placeholder="Format preference" aria-label="Format preference"><label class="check"><input id="preview-auto" type="checkbox"> Auto-monitor</label><button type="submit">Build payload</button></form><pre id="preview-output" class="output">Expected upstream payload will appear here.</pre></article>
</section>
<section class="tab-panel" id="reconcile">
<article class="panel"><div class="panel-head"><div><h2>Reconcile dashboard</h2><p class="muted">Non-terminal request states should be checked against BookWarehouse and the scheduled reconciler.</p></div></div><div id="reconcile-output" class="cards muted">Loading request stats...</div></article>
</section>
<section class="panel"><h2>Request triage</h2><div class="triage-grid">
<div><h3>Upstream conflicts</h3><p>Authentication, schema, duplicate request, and quality-profile errors are upstream contract problems; confirm with test search before changing portal routing.</p></div>
<div><h3>Reconcile stuck requests</h3><p>Rows stuck in non-terminal states should be checked against the BookWarehouse request id and the scheduled reconciler status.</p></div>
<div><h3>Auto monitoring</h3><p>When auto monitoring is enabled, verify the configured quality profile and expected payload before assuming a missing ebook is a portal issue.</p></div>
</div></section>
<section class="panel"><h2>Operations checklist</h2><ul><li>Configure <code>database_url</code>, <code>base_url</code>, and <code>api_key</code>.</li><li>Confirm this backend is selected by the Ebooks portal.</li><li>Use test search to validate BookWarehouse API access.</li><li>Check request stats before assuming the portal is at fault.</li></ul></section>
</main>
<script>
const statusEl=document.getElementById("status"), output=document.getElementById("search-output"), reconcileOutput=document.getElementById("reconcile-output"), previewOutput=document.getElementById("preview-output");
const hostToken=new URLSearchParams(location.search).get("token")||"";
function headers(){return hostToken?{Authorization:"Bearer "+hostToken}:{}}
function badge(ok){return '<span class="badge '+(ok?'ok':'bad')+'">'+(ok?'OK':'Needs attention')+'</span>'}
function esc(v){return String(v??"").replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function activateTab(id){document.querySelectorAll(".tab").forEach(b=>b.classList.toggle("active",b.dataset.tabTarget===id));document.querySelectorAll(".tab-panel").forEach(p=>p.classList.toggle("active",p.id===id))}
document.querySelectorAll(".tab").forEach(b=>b.addEventListener("click",()=>activateTab(b.dataset.tabTarget)))
function statCard(label,value,ok){return '<div class="diag">'+badge(ok)+'<strong>'+esc(label)+'</strong><span>'+esc(value)+'</span></div>'}
async function load(){try{const r=await fetch("./api/v1/admin/diagnostics",{headers:headers()});const d=await r.json();const ready=d.configured&&d.database?.ok&&d.upstream?.ok;document.getElementById("ready-badge").textContent=ready?"Ready":"Needs attention";statusEl.innerHTML=statCard("Configured",d.configured?"base_url, api_key, database_url applied":"missing required configuration",d.configured)+statCard("Database",d.database?.message||"not configured",d.database?.ok)+statCard("BookWarehouse",d.upstream?.message||"not configured",d.upstream?.ok)+statCard("Auto monitoring",String(d.auto_monitoring_enabled),true);const req=d.requests||{};reconcileOutput.innerHTML=statCard("Non-terminal request states",JSON.stringify(req),true)+statCard("Quality profile",d.request_quality_profile||"upstream default",true)+statCard("Base URL",d.base_url||"not set",Boolean(d.base_url))}catch(e){statusEl.textContent=String(e);reconcileOutput.textContent=String(e)}} 
document.getElementById("search-form").addEventListener("submit",async e=>{e.preventDefault();output.textContent="Searching...";try{const q=encodeURIComponent(document.getElementById("q").value||"foundation");const r=await fetch("./api/v1/admin/test-search?q="+q,{headers:headers()});output.textContent=JSON.stringify(await r.json(),null,2)}catch(err){output.textContent=String(err)}})
document.getElementById("request-preview-form").addEventListener("submit",e=>{e.preventDefault();const payload={title:document.getElementById("preview-title").value.trim(),authors:document.getElementById("preview-authors").value.split(",").map(v=>v.trim()).filter(Boolean),isbn:document.getElementById("preview-isbn").value.trim(),format_pref:document.getElementById("preview-format").value.trim(),auto_monitor:document.getElementById("preview-auto").checked};previewOutput.textContent=JSON.stringify({expected_upstream_payload:payload},null,2)})
load();
</script>
</body></html>`))
}

func adminTheme(r *http.Request) string {
	theme := r.Header.Get("X-Continuum-Theme")
	if theme == "" {
		theme = r.URL.Query().Get("theme")
	}
	if theme == "" {
		theme = "default"
	}
	return html.EscapeString(theme)
}

func adminThemeCSS() string {
	return `:root{--bg:#141417;--fg:#e8e8ec;--muted:#a1a1aa;--link:#93c5fd;--panel:#1c1c20;--border:#28282e;--ok:#22c55e;--bad:#fb7185;--input:#101014}[data-theme="cinema-light"]{--bg:#f7f3ed;--fg:#201c18;--muted:#756b60;--link:#9a3412;--panel:#fffaf3;--border:#ded1c0;--input:#fff}[data-theme="cobalt-studio"]{--bg:#101623;--fg:#eef4ff;--muted:#afc2e2;--link:#60a5fa;--panel:#172033;--border:#2d3f61;--input:#0d1422}[data-theme="oxblood-noir"]{--bg:#170b10;--fg:#fff1f4;--muted:#f0a6b7;--link:#fb7185;--panel:#241018;--border:#4a2230;--input:#12070b}[data-theme="evergreen-studio"]{--bg:#0d1712;--fg:#ecfdf3;--muted:#9bd6b4;--link:#6ee7b7;--panel:#14241b;--border:#2b4b39;--input:#08110d}*{box-sizing:border-box}body{font-family:system-ui,sans-serif;margin:0;line-height:1.5;background:var(--bg);color:var(--fg)}.shell{max-width:1120px;margin:0 auto;padding:28px}.back{display:inline-flex;margin-bottom:12px;color:var(--link);text-decoration:none}.eyebrow{color:var(--muted);text-transform:uppercase;font-size:12px;letter-spacing:.08em}h1{margin:.2rem 0}h2{font-size:16px;margin:0}.tabs{display:flex;gap:8px;flex-wrap:wrap;margin:18px 0}.tab{background:transparent;color:var(--fg);border:1px solid var(--border)}.tab.active{background:var(--link);color:#08111f}.tab-panel{display:none}.tab-panel.active{display:block}.grid,.triage-grid,.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:16px}.panel{border:1px solid var(--border);background:var(--panel);border-radius:8px;padding:16px;margin-top:16px}.panel-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.triage-grid h3{font-size:14px;margin:.2rem 0}.triage-grid p{color:var(--muted);margin:.25rem 0}.stack>*+*{margin-top:8px}.row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px}.preview-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.check{display:flex;align-items:center;gap:8px;color:var(--muted)}input{min-width:0;background:var(--input);color:var(--fg);border:1px solid var(--border);border-radius:6px;padding:9px}input[type="checkbox"]{width:auto}button{background:var(--link);border:0;border-radius:6px;padding:9px 12px;color:#08111f;font-weight:700;cursor:pointer}.badge{display:inline-block;border:1px solid var(--border);border-radius:999px;padding:2px 8px;margin-right:6px;font-size:12px;white-space:nowrap}.ok{color:var(--ok)}.bad{color:var(--bad)}.muted{color:var(--muted)}.diag{display:grid;gap:4px;border:1px solid var(--border);border-radius:6px;background:var(--input);padding:12px}.diag strong{color:var(--fg)}.diag span{color:var(--muted);font-size:12px}.output{overflow:auto;max-height:340px;background:var(--input);border:1px solid var(--border);border-radius:6px;padding:10px;color:var(--fg)}code{color:var(--link)}@media(max-width:760px){.row,.preview-grid,.panel-head{grid-template-columns:1fr;display:grid}}`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	upstreamOK := false
	upstreamMessage := "not configured"
	if s.deps.BookwarehouseClient != nil {
		if err := s.deps.BookwarehouseClient.Ping(ctx); err != nil {
			upstreamMessage = err.Error()
		} else {
			upstreamOK = true
			upstreamMessage = "upstream reachable"
		}
	}
	dbOK := false
	dbMessage := "not configured"
	var stats any = map[string]any{}
	if s.deps.Store != nil {
		if err := s.deps.Store.Pool().Ping(ctx); err != nil {
			dbMessage = err.Error()
		} else {
			dbOK = true
			dbMessage = "database reachable"
		}
		if requestStats, err := s.deps.Store.RequestStats(ctx); err == nil {
			stats = requestStats
		}
	}
	writeJSON(w, 200, map[string]any{
		"plugin_id":               "continuum.bookwarehouse-ebook",
		"role":                    "library_source_and_download_provider",
		"configured":              s.deps.Config.Configured(),
		"base_url":                s.deps.Config.BaseURL,
		"auto_monitoring_enabled": s.deps.EnableAutoMonitoring,
		"request_quality_profile": s.deps.Config.RequestQualityProfile,
		"upstream": map[string]any{
			"ok":      upstreamOK,
			"message": upstreamMessage,
		},
		"database": map[string]any{
			"ok":      dbOK,
			"message": dbMessage,
		},
		"requests": stats,
	})
}

func (s *Server) handleTestSearch(w http.ResponseWriter, r *http.Request) {
	if s.deps.BookwarehouseClient == nil {
		writeJSON(w, 200, map[string]any{"ok": false, "message": "not configured", "items": []any{}})
		return
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "foundation"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	hits, err := s.deps.BookwarehouseClient.ExternalSearch(ctx, query, 5)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error(), "items": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "search completed", "items": hits})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
