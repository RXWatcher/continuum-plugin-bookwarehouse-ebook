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

	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/catalog"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/request"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/runtime"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/store"
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
		r.Get("/admin/config", s.handleGetConfig)
		r.Patch("/admin/config", s.handleUpdateConfig)
		r.Get("/admin/test-search", s.handleTestSearch)
		if s.deps.BookwarehouseClient != nil {
			ch := catalog.NewHandler(s.deps.BookwarehouseClient, s.deps.Config.StreamSigningSecret)
			ch.Mount(r)
			rh := request.NewHandler(s.deps.BookwarehouseClient)
			r.Get("/requests/{external_id}", chiPathValueShim(rh.Snapshot()).ServeHTTP)
		}
	})
	return r
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "store not configured"})
		return
	}
	cfg, err := s.deps.Store.GetAppConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	cfg.APIKey = ""
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "store not configured"})
		return
	}
	cur, err := s.deps.Store.GetAppConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var next runtime.Config
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if next.APIKey == "" {
		next.APIKey = cur.APIKey
	}
	if err := s.deps.Store.UpdateAppConfig(r.Context(), next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	next.DatabaseURL = s.deps.Config.DatabaseURL
	s.deps.Config = next
	s.deps.EnableAutoMonitoring = next.EnableAutoMonitoring
	if s.deps.BookwarehouseClient != nil {
		s.deps.BookwarehouseClient.Reconfigure(next.BaseURL, next.APIKey)
		s.deps.BookwarehouseClient.SetDefaultCoverSize(next.DefaultCoverSize)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
<div id="status-strip" class="status-strip" aria-label="Plugin health"><div class="strip-cell"><span class="strip-dot"></span><strong>Loading…</strong></div></div>
<nav class="tabs" aria-label="BookWarehouse Ebook admin sections">
<button class="tab active" data-tab-target="readiness" type="button">Readiness</button>
<button class="tab" data-tab-target="config" type="button">Config</button>
<button class="tab" data-tab-target="browser" type="button">Browser</button>
<button class="tab" data-tab-target="request-preview" type="button">Request preview</button>
<button class="tab" data-tab-target="reconcile" type="button">Reconcile</button>
</nav>
<section class="tab-panel active" id="readiness">
<article class="panel"><div class="panel-head"><div><h2>Setup status</h2><p class="muted">Confirms database, upstream API, auto-monitoring, and current request health.</p></div><span id="ready-badge" class="badge">Loading</span></div><div id="status" class="cards muted">Loading diagnostics...</div></article>
</section>
<section class="tab-panel" id="config">
<article class="panel"><div class="panel-head"><div><h2>Plugin config</h2><p class="muted">BookWarehouse connection, request defaults, and monitoring behavior live in this plugin database.</p></div><span id="config-state" class="badge">Loading</span></div><form id="config-form" class="config-grid"><label>Base URL<input id="cfg-base-url" placeholder="https://bookwarehouse.domain.com"></label><label>API key<input id="cfg-api-key" type="password" placeholder="Leave blank to keep current key"></label><label>Default cover size<select id="cfg-cover-size"><option value="medium">Medium</option><option value="original">Original</option><option value="thumbnail">Thumbnail</option></select></label><label>Request quality profile<input id="cfg-quality-profile" placeholder="Optional upstream profile"></label><label class="check span-all"><input id="cfg-auto" type="checkbox"> Enable auto monitoring</label><button type="submit">Save config</button></form></article>
</section>
<section class="tab-panel" id="browser">
<article class="panel"><div class="panel-head"><div><h2>Backend browser</h2><p class="muted">Search BookWarehouse before changing portal routing or request policy.</p></div><span id="search-state" class="badge">Idle</span></div><form id="search-form" class="row"><input id="q" value="foundation" placeholder="Search title or author" aria-label="Search query"><button type="submit">Test search</button></form><div class="browser-shell"><div id="search-summary" class="info-grid"><div class="diag"><strong>Ready</strong><span>Search results will render here as operator-facing cards instead of raw JSON.</span></div></div><div id="search-results" class="result-grid"><div class="empty-state">No test run yet.</div></div></div></article>
</section>
<section class="tab-panel" id="request-preview">
<article class="panel"><div class="panel-head"><div><h2>Request preview</h2><p class="muted">Build the expected upstream payload before enabling request forwarding for users.</p></div><span id="preview-state" class="badge">Idle</span></div><form id="request-preview-form" class="preview-grid"><input id="preview-title" placeholder="Title" aria-label="Title"><input id="preview-authors" placeholder="Authors, comma separated" aria-label="Authors"><input id="preview-isbn" placeholder="ISBN" aria-label="ISBN"><input id="preview-format" placeholder="Format preference" aria-label="Format preference"><label class="check"><input id="preview-auto" type="checkbox"> Auto-monitor</label><button type="submit">Build payload</button></form><div id="preview-summary" class="info-grid"><div class="diag"><strong>Expected payload</strong><span>Fill in the fields above to preview the upstream request body.</span></div></div></article>
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
const statusEl=document.getElementById("status"), searchState=document.getElementById("search-state"), searchSummary=document.getElementById("search-summary"), searchResults=document.getElementById("search-results"), reconcileOutput=document.getElementById("reconcile-output"), previewSummary=document.getElementById("preview-summary"), previewState=document.getElementById("preview-state"), configState=document.getElementById("config-state");
const hostToken=new URLSearchParams(location.search).get("token")||"";
function headers(){return hostToken?{Authorization:"Bearer "+hostToken}:{}}
function badge(ok){return '<span class="badge '+(ok?'ok':'bad')+'">'+(ok?'OK':'Needs attention')+'</span>'}
function esc(v){return String(v??"").replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function activateTab(id){document.querySelectorAll(".tab").forEach(b=>b.classList.toggle("active",b.dataset.tabTarget===id));document.querySelectorAll(".tab-panel").forEach(p=>p.classList.toggle("active",p.id===id))}
document.querySelectorAll(".tab").forEach(b=>b.addEventListener("click",()=>activateTab(b.dataset.tabTarget)))
function statCard(label,value,ok){return '<div class="diag">'+badge(ok)+'<strong>'+esc(label)+'</strong><span>'+esc(value)+'</span></div>'}
function renderStrip(cells){const strip=document.getElementById("status-strip");if(!strip)return;strip.replaceChildren();for(const c of cells){const cell=document.createElement("div");cell.className="strip-cell "+(c.ok?"ok":"bad");if(c.detail)cell.title=c.detail;const dot=document.createElement("span");dot.className="strip-dot";const label=document.createElement("strong");label.textContent=c.label;const note=document.createElement("span");note.textContent=c.ok?"OK":"Check";cell.append(dot,label,note);strip.appendChild(cell)}}
async function readMaybeJSON(r){const text=await r.text();const type=r.headers.get("content-type")||"";if(type.includes("application/json")){try{return text?JSON.parse(text):{}}catch{}}return {error:text||r.statusText,raw:text}}
function normalizeCoverSize(v){switch(String(v||"").trim().toLowerCase()){case"thumbnail":case"small":return"thumbnail";case"original":case"large":return"original";case"medium":return"medium";default:return"medium"}}
function formatList(list){return Array.isArray(list)&&list.length?list.join(', '):'Not provided'}
function coverHTML(src,label){if(src){return '<img src="'+esc(src)+'" alt="'+esc(label)+'">'}return '<div class="cover-fallback">BOOK</div>'}
function renderInfoGrid(items){return items.map(item=>'<div class="diag"><strong>'+esc(item.label)+'</strong><span>'+esc(item.value)+'</span></div>').join('')}
function renderSearchResults(data,query){const items=Array.isArray(data.items)?data.items:[];searchState.textContent=items.length?items.length+' results':'No matches';searchSummary.innerHTML=renderInfoGrid([{label:'Query',value:query||'foundation'},{label:'Results shown',value:items.length},{label:'Search status',value:data.ok===false?(data.message||'Failed'):(data.message||'Completed')}]);searchResults.innerHTML=items.length?items.map(item=>'<article class="result-card">'+coverHTML(item.cover_url,item.title||'Cover')+'<div class="result-body"><strong>'+esc(item.title||'Untitled')+'</strong><span class="muted">'+esc(formatList(item.authors))+'</span><div class="meta-line">'+esc(item.source||'Unknown source')+(item.year?' · '+esc(item.year):'')+(item.language?' · '+esc(item.language):'')+'</div><div class="chip-row">'+(Array.isArray(item.formats)?item.formats.map(format=>'<span class="chip">'+esc(format)+'</span>').join(''):'')+'</div></div></article>').join(''):'<div class="empty-state">No books matched this search.</div>'}
function renderPreview(payload){previewState.textContent='Built';previewSummary.innerHTML=renderInfoGrid([{label:'Title',value:payload.title||'Not provided'},{label:'Authors',value:payload.authors.length?payload.authors.join(', '):'Not provided'},{label:'ISBN',value:payload.isbn||'Not provided'},{label:'Format preference',value:payload.format_pref||'Upstream default'},{label:'Auto-monitor',value:payload.auto_monitor?'Enabled':'Disabled'}])}
async function loadConfig(){try{const r=await fetch("./api/v1/admin/config",{headers:headers()});const d=await readMaybeJSON(r);if(!r.ok)throw new Error(d.error||r.statusText);document.getElementById("cfg-base-url").value=d.base_url||"https://bookwarehouse.domain.com";document.getElementById("cfg-cover-size").value=normalizeCoverSize(d.default_cover_size);document.getElementById("cfg-quality-profile").value=d.request_quality_profile||"";document.getElementById("cfg-auto").checked=!!d.enable_auto_monitoring;configState.textContent="Loaded"}catch(e){configState.textContent="Unavailable"}}
async function load(){try{const r=await fetch("./api/v1/admin/diagnostics",{headers:headers()});const d=await r.json();const ready=d.configured&&d.database?.ok&&d.upstream?.ok;document.getElementById("ready-badge").textContent=ready?"Ready":"Needs attention";statusEl.innerHTML=statCard("Configured",d.configured?"base_url, api_key, database_url applied":"missing required configuration",d.configured)+statCard("Database",d.database?.message||"not configured",d.database?.ok)+statCard("BookWarehouse",d.upstream?.message||"not configured",d.upstream?.ok)+statCard("Auto monitoring",String(d.auto_monitoring_enabled),true);const req=d.requests||{};reconcileOutput.innerHTML=statCard("Total requests",req.total||0,true)+statCard("Active",req.active||0,true)+statCard("Failed",req.failed||0,(req.failed||0)===0)+statCard("Imported",req.imported||0,true)+statCard("With errors",req.with_errors||0,(req.with_errors||0)===0)+statCard("Unsubmitted",req.unsubmitted||0,(req.unsubmitted||0)===0)+statCard("Quality profile",d.request_quality_profile||"upstream default",true)+statCard("Base URL",d.base_url||"not set",Boolean(d.base_url));renderStrip([{label:'DB',ok:!!d.database?.ok,detail:d.database?.message},{label:'Configured',ok:!!d.configured,detail:'base_url + api_key + database_url'},{label:'BookWarehouse',ok:!!d.upstream?.ok,detail:d.upstream?.message},{label:'Auto monitor',ok:!!d.auto_monitoring_enabled,detail:d.auto_monitoring_enabled?'enabled':'disabled'},{label:'Failed reqs',ok:(req.failed||0)===0,detail:(req.failed||0)+' failed'}])}catch(e){statusEl.textContent=String(e);reconcileOutput.innerHTML='<div class="empty-state">'+esc(String(e))+'</div>';renderStrip([{label:'Diagnostics',ok:false,detail:String(e)}])}}
document.getElementById("config-form").addEventListener("submit",async e=>{e.preventDefault();configState.textContent="Saving";try{const body={base_url:document.getElementById("cfg-base-url").value.trim(),api_key:document.getElementById("cfg-api-key").value,default_cover_size:document.getElementById("cfg-cover-size").value||"medium",request_quality_profile:document.getElementById("cfg-quality-profile").value.trim(),enable_auto_monitoring:document.getElementById("cfg-auto").checked};const r=await fetch("./api/v1/admin/config",{method:"PATCH",headers:{...headers(),"Content-Type":"application/json"},body:JSON.stringify(body)});const d=await readMaybeJSON(r);if(!r.ok)throw new Error(d.error||r.statusText);document.getElementById("cfg-api-key").value="";configState.textContent="Saved";await loadConfig()}catch(err){configState.textContent="Error"}})
document.getElementById("search-form").addEventListener("submit",async e=>{e.preventDefault();const rawQuery=document.getElementById("q").value||"foundation";searchState.textContent="Searching";searchResults.innerHTML='<div class="empty-state">Searching BookWarehouse...</div>';try{const q=encodeURIComponent(rawQuery);const r=await fetch("./api/v1/admin/test-search?q="+q,{headers:headers()});const d=await readMaybeJSON(r);if(!r.ok)throw new Error(d.error||r.statusText);renderSearchResults(d,rawQuery)}catch(err){searchState.textContent="Error";searchSummary.innerHTML='';searchResults.innerHTML='<div class="empty-state">'+esc(String(err))+'</div>'}})
document.getElementById("request-preview-form").addEventListener("submit",e=>{e.preventDefault();const payload={title:document.getElementById("preview-title").value.trim(),authors:document.getElementById("preview-authors").value.split(",").map(v=>v.trim()).filter(Boolean),isbn:document.getElementById("preview-isbn").value.trim(),format_pref:document.getElementById("preview-format").value.trim(),auto_monitor:document.getElementById("preview-auto").checked};renderPreview(payload)})
load();loadConfig();
</script>
</body></html>`))
}

func adminTheme(r *http.Request) string {
	theme := r.Header.Get("X-Silo-Theme")
	if theme == "" {
		theme = r.URL.Query().Get("theme")
	}
	if theme == "" {
		theme = "default"
	}
	return html.EscapeString(theme)
}

func adminThemeCSS() string {
	return `:root{--bg:#141417;--fg:#e8e8ec;--muted:#a1a1aa;--link:#93c5fd;--panel:#1c1c20;--border:#28282e;--ok:#22c55e;--bad:#fb7185;--input:#101014}[data-theme="cinema-light"]{--bg:#f7f3ed;--fg:#201c18;--muted:#756b60;--link:#9a3412;--panel:#fffaf3;--border:#ded1c0;--input:#fff}[data-theme="cobalt-studio"]{--bg:#101623;--fg:#eef4ff;--muted:#afc2e2;--link:#60a5fa;--panel:#172033;--border:#2d3f61;--input:#0d1422}[data-theme="oxblood-noir"]{--bg:#170b10;--fg:#fff1f4;--muted:#f0a6b7;--link:#fb7185;--panel:#241018;--border:#4a2230;--input:#12070b}[data-theme="evergreen-studio"]{--bg:#0d1712;--fg:#ecfdf3;--muted:#9bd6b4;--link:#6ee7b7;--panel:#14241b;--border:#2b4b39;--input:#08110d}*{box-sizing:border-box}body{font-family:system-ui,sans-serif;margin:0;line-height:1.5;background:var(--bg);color:var(--fg)}.shell{max-width:1120px;margin:0 auto;padding:28px}.back{display:inline-flex;margin-bottom:12px;color:var(--link);text-decoration:none}.eyebrow{color:var(--muted);text-transform:uppercase;font-size:12px;letter-spacing:.08em}h1{margin:.2rem 0}h2{font-size:16px;margin:0}.tabs{display:flex;gap:8px;flex-wrap:wrap;margin:18px 0}.tab{background:transparent;color:var(--fg);border:1px solid var(--border)}.tab.active{background:var(--link);color:#08111f}.tab-panel{display:none}.tab-panel.active{display:block}.grid,.triage-grid,.cards,.info-grid,.result-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:16px}.panel{border:1px solid var(--border);background:var(--panel);border-radius:8px;padding:16px;margin-top:16px}.panel-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.triage-grid h3{font-size:14px;margin:.2rem 0}.triage-grid p{color:var(--muted);margin:.25rem 0}.browser-shell{display:grid;gap:10px}.row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px}.preview-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.config-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-top:12px}.config-grid label{display:grid;gap:6px;color:var(--muted);font-size:13px}.config-grid .span-all{grid-column:1/-1}.check{display:flex!important;align-items:center;gap:8px;color:var(--muted)}select,input{min-width:0;background:var(--input);color:var(--fg);border:1px solid var(--border);border-radius:6px;padding:9px}input[type="checkbox"]{width:auto}button{background:var(--link);border:0;border-radius:6px;padding:9px 12px;color:#08111f;font-weight:700;cursor:pointer}.badge{display:inline-block;border:1px solid var(--border);border-radius:999px;padding:2px 8px;margin-right:6px;font-size:12px;white-space:nowrap}.ok{color:var(--ok)}.bad{color:var(--bad)}.muted{color:var(--muted)}.diag{display:grid;gap:4px;border:1px solid var(--border);border-radius:6px;background:var(--input);padding:12px}.diag strong{color:var(--fg)}.diag span{color:var(--muted);font-size:12px}.result-card{display:grid;grid-template-columns:72px minmax(0,1fr);gap:12px;border:1px solid var(--border);border-radius:8px;background:var(--input);padding:12px}.result-card img,.cover-fallback{width:72px;height:96px;border-radius:6px;object-fit:cover;background:#0b0f17}.cover-fallback{display:grid;place-items:center;color:var(--muted);font-size:12px;font-weight:700;letter-spacing:.06em}.result-body{display:grid;gap:6px;min-width:0}.result-body strong{font-size:14px}.meta-line{color:var(--muted);font-size:12px}.chip-row{display:flex;flex-wrap:wrap;gap:6px}.chip{border:1px solid var(--border);border-radius:999px;padding:3px 8px;font-size:12px;color:var(--muted)}.empty-state{border:1px dashed var(--border);border-radius:6px;padding:12px;background:var(--input);color:var(--muted);font-size:13px}code{color:var(--link)}.status-strip{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:8px;margin-top:14px}.strip-cell{display:flex;align-items:center;gap:8px;border:1px solid var(--border);background:var(--panel);border-radius:6px;padding:8px 10px;font-size:12px}.strip-dot{flex:0 0 8px;width:8px;height:8px;border-radius:999px;background:var(--muted)}.strip-cell.ok .strip-dot{background:var(--ok)}.strip-cell.bad .strip-dot{background:var(--bad)}.strip-cell strong{font-weight:600;color:var(--fg)}.strip-cell span{color:var(--muted);margin-left:auto;font-size:11px}@media(max-width:760px){.row,.preview-grid,.panel-head,.config-grid,.result-card{grid-template-columns:1fr;display:grid}.result-card img,.cover-fallback{width:100%;max-width:120px}}`
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
		"plugin_id":               "silo.bookwarehouse-ebook",
		"role":                    "library_source_and_download_provider",
		"configured":              s.deps.Config.ProviderConfigured(),
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
