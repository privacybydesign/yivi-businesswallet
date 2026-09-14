package main

import "html/template"

const pageStyle = `<style>
body{font:15px/1.5 system-ui,sans-serif;max-width:56rem;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
h1{font-size:1.4rem}code,pre{font:13px ui-monospace,monospace;background:#f3f1ee;border-radius:6px}
pre{padding:.75rem;overflow:auto;white-space:pre-wrap;word-break:break-all}code{padding:.1rem .3rem}
label{display:block;margin:.75rem 0 .25rem;font-weight:600}input,select{width:100%;padding:.5rem;font:inherit}
button,.btn{display:inline-block;margin-top:1rem;padding:.6rem 1rem;background:#b21e5a;color:#fff;border:0;border-radius:6px;font:inherit;text-decoration:none}
table{border-collapse:collapse;width:100%;margin:.5rem 0}td,th{text-align:left;padding:.35rem .5rem;border-bottom:1px solid #e4e2df;vertical-align:top}
.ok{color:#1b7f3b;font-weight:600}.bad{color:#b3261e;font-weight:600}.muted{color:#6b6b6b}
</style>`

var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html><title>Dev verifier</title>` + pageStyle + `
<h1>Dev verifier</h1>
<p>Acts as an OpenID4VP relying party (<code>{{.ClientID}}</code>) towards the business wallet: pick what to ask
for, open the link on the next page, sign in to the wallet and choose the organization that answers.</p>
<form method="post" action="/sessions">
<label for="vct">Credential type (vct)</label>
<input id="vct" name="vct" value="{{.DefaultVCT}}" required>
<label for="claims">Claims to disclose (comma-separated, empty = none: the wallet only proves it holds the credential)</label>
<input id="claims" name="claims" placeholder="legalName, kvkNumber">
<label for="response_mode">Response mode</label>
<select id="response_mode" name="response_mode">
<option value="direct_post">direct_post</option>
<option value="direct_post.jwt">direct_post.jwt (encrypted)</option>
</select>
<button type="submit">Create request</button>
</form>`))

var sessionTemplate = template.Must(template.New("session").Parse(`<!doctype html><title>Dev verifier · request</title>` + pageStyle + `
{{if not .Done}}<meta http-equiv="refresh" content="{{.Refresh}}">{{end}}
<h1>Presentation request</h1>
<table>
<tr><th>vct</th><td><code>{{.VCT}}</code></td></tr>
<tr><th>claims</th><td>{{if .Claims}}<code>{{.Claims}}</code>{{else}}<span class="muted">all</span>{{end}}</td></tr>
<tr><th>response_mode</th><td><code>{{.ResponseMode}}</code></td></tr>
<tr><th>request object</th><td>{{if .Fetched}}fetched by the wallet{{else}}<span class="muted">not fetched yet</span>{{end}}</td></tr>
</table>
{{if .Done}}
  {{if .Failure}}<p class="bad">Response refused: {{.Failure}}</p>
  {{else}}
  <p class="ok">Response received.</p>
  {{range .Presented}}
    <h2><code>{{.VCT}}</code> <span class="muted">for query {{.QueryID}}</span></h2>
    <p>Issuer: <code>{{.Issuer}}</code> —
    {{if .Verified}}<span class="ok">verified</span> (issuer chain, disclosures, key binding, nonce, audience)
    {{else}}<span class="bad">NOT verified</span>: {{.Error}}{{end}}</p>
    <table><tr><th>claim</th><th>value</th></tr>
    {{range $k, $v := .Claims}}<tr><td><code>{{$k}}</code></td><td>{{$v}}</td></tr>{{end}}
    </table>
    <details><summary>raw presentation</summary><pre>{{.Raw}}</pre></details>
  {{end}}
  {{end}}
  <p><a class="btn" href="/">New request</a></p>
{{else}}
  <p>Open this in the browser you use for the business wallet:</p>
  <p><a class="btn" href="{{.Invocation}}" target="_blank" rel="noopener">Share with the business wallet</a></p>
  <pre>{{.Invocation}}</pre>
  <p class="muted">This page refreshes until the wallet has answered.</p>
{{end}}`))
