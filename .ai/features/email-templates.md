# Transactional mail templates and the branded mail shell

**Status:** rendering layer implemented (this is the first slice of #132). Tenant editing,
storage and the editor UI are not built yet; see §7.
**Slice:** `internal/email` composition only. The SMTP transport (`internal/mailer`) and the
per-org SMTP settings model are untouched.

---

## 1. What this is

Every transactional mail the backend sends is composed here: the credential offer, the member
invitation, the PostGuard "own SMTP" notification and the SMTP self-test. Before this slice each
of those was a pair of `fmt.Sprintf` string literals in `service.go` with an English subject and
a bare `<p>` + `<a>` body, so mail was the one product surface that carried no tenant branding
and no Dutch.

Now `service.go` owns no copy at all. A send is:

```
caller ─▶ Service.send(kind, vars)
             ├─ DefaultTemplate(kind, locale)   catalog.go   copy, as data
             ├─ brandSource.MailBrandSeeds()    (org_theme_settings, via cmd/api)
             ├─ resolveBrand(seeds)             brand.go     WCAG-AA palette
             └─ Render(kind, locale, tpl, brand, vars)
                  ├─ ValidateTemplate           render.go    allowlisted placeholders
                  ├─ substitute + escape        render.go    per MIME part
                  └─ renderHTML / renderText    shell.go     one branded layout
```

---

## 2. The catalogue: kinds are code, copy is data

`catalog.go` enumerates a closed set of `Kind`s, each declaring exactly the variables its
templates may reference. Adding a cause is a code change (a caller has to supply the variables);
changing what a cause *says* is a data change.

| kind | trigger | variables |
|---|---|---|
| `credential_offer` | `attestation.Service` issues to a natural person | `orgName`, `credentialName`, `claimUrl`, `txCode` |
| `invitation` | org admin invites a member | `orgName`, `acceptUrl` |
| `postguard_file` | PostGuard "own SMTP" delivery | `orgName`, `message`, `downloadUrl` |
| `smtp_test` | `POST /email/test` | `orgName` |

The variable list is what a caller actually passes today. The issue also lists `inviterName` for
the invitation; `organization.Handler` does not carry the inviter into the send, so declaring it
would mean a template could reference a value that is never supplied. It gets added together with
the caller change, not before.

The shipped copy lives in `templates/defaults.<locale>.json`, `go:embed`ed and loaded once at
package init. A missing kind, an unknown kind, an undeclared placeholder or an empty shell string
panics at init: the files are committed data, so any of those is a build mistake, and every test
run in the package initialises it.

## 3. Templates are prose, not HTML

A `Template` is `subject`, `preheader`, `headline`, `paragraphs`, `ctaLabel` + `ctaUrl`,
`linkFallback`, `note`, `footer`. There is deliberately **no HTML body field**.

That is the central design decision, and it settles the question raised on #132 about authoring
the shell with react-email / MJML / Maizzle and compiling it to Go:

- **A tenant never authors HTML**, so no tenant can break the mail-client-safe layout, and
  there is no HTML sanitiser to get wrong.
- **The `text/plain` alternative is generated, not required.** Both parts come from the same
  resolved content, so they cannot say different things and a tenant cannot leave one behind.
- **No Node in the build.** The layout is one hand-written Go function (`shell.go`), pinned by
  tests, rather than generated output that a CI drift check has to keep honest. A compile step
  buys component ergonomics for markup a tenant cannot touch and nobody edits per release; the
  cost is a second toolchain plus a drift guard. If the shell ever grows past what one function
  can carry, revisit — the tenant-facing contract (prose fields) does not change if it does.

Rendering is not `text/template` execution. Placeholders are `{{name}}`, resolved against the
kind's declared variables and nothing else:

- an undeclared placeholder, or a malformed one like `{{ org name }}`, is a validation error at
  save time and again at render time, never a silent blank;
- values are escaped per part (HTML-escaped in the HTML alternative, raw in the text one), and
  the template prose is escaped the same way, so tenant text cannot introduce markup;
- URL variables must parse as absolute `http(s)` URLs with a host, so a call to action can never
  be relative or a `javascript:` scheme;
- the subject is collapsed to a single line before `internal/mailer`'s header sanitising sees it;
- a block whose every placeholder resolves to an empty value is dropped whole, which is how an
  optional part disappears instead of leaving a dangling label ("Your wallet will ask for this
  code: " with no code).

## 4. Branding: one place to configure, mirrored in Go

`brand.go` derives the mail palette from the org's existing `org_theme_settings` row. A tenant
configures branding once, in theme settings, and mail follows; there is no second palette.

Mail renders server-side, so the derivation the frontend does in `frontend/src/lib/theme.ts`
exists a second time in Go. The two are held to the same rules (WCAG relative luminance, the same
near-white / warm-dark foreground candidates, the same step size when nudging toward the contrast
floor) and `brand_test.go` asserts AA over a matrix of seed colours, so drift shows up as a
failing test rather than as unreadable mail. Only the **light** half is mirrored: mail has no dark
override to serve (§5), so there is no dark derivation to keep in sync.

The floors the test pins, for any seed a tenant can save: body text, muted footer text and links
against the card, and the CTA label against the primary fill. The theme settings form does not
gate contrast for mail, so the derivation has to. Note the mail footer uses an AA-guaranteed muted
value rather than mirroring the app's `--yb-muted`, which deliberately runs below AA for
incidental captions — the footer is where a recipient learns who sent the message.

A brand lookup that fails logs and falls back to the default Yivi palette. Mail without the
tenant's colours is a cosmetic loss; mail not sent is a broken flow.

## 5. The shell obeys the mail-client baseline

`shell.go` is the only HTML in the package. Mail clients are not browsers, so:

- table-based layout, no flex/grid, `width:100%;max-width:600px` instead of a media query;
- every declaration inline, no `<style>` (Gmail strips it), no classes, no custom properties —
  the app's `--yb-*` tokens and its `:root:root` override block do not survive an inbox;
- a `bgcolor` attribute beside each background declaration, for clients that drop CSS backgrounds
  on table cells;
- one light palette, chosen to survive a dark-mode client's inversion (dark text on a near-white
  card, foregrounds nudged toward black). There is no `prefers-color-scheme` half to ship;
- layout tables carry `role="presentation"` and the message carries `lang` from its locale, so a
  screen reader does not announce the mail as a data grid in the wrong language;
- the bare call-to-action URL is printed under the button, for a client that will not render it.

`shell_test.go` asserts the forbidden constructs are absent and the required ones present, which
is the only guard that survives a future edit — a browser preview would look fine either way.

## 6. Locale

Templates are keyed per locale (`en`, `nl`, matching `frontend/src/i18n/locales/`).
`ResolveLocale(preferences...)` takes preferences in **recipient → organization → `en`** order.

Today the only preference that exists is `MAIL_DEFAULT_LOCALE`, the deployment default (validated
against the shipped locales at boot in `cmd/api`, so a typo fails the deploy rather than the first
send). Neither a per-user language preference nor a per-org default is stored yet; when either
lands it becomes an earlier argument to the same call, and nothing else moves.

## 7. Not built yet

The remainder of #132, in the order it makes sense to land:

1. **Tenant editing** — `org_email_templates` migration (`(org_id, kind, locale)` unique),
   store, `GET|PUT|DELETE /orgs/{slug}/email/templates/{kind}/{locale}` (DELETE reverts to the
   shipped default), a `POST .../preview` that renders with sample variables, `kind` on
   `POST /email/test`, `email.template_updated` / `email.template_reset` audit actions with their
   `en`/`nl` translations and `audit-event.ts` case, and the OpenAPI spec. `ValidateTemplate` is
   already the save-time check this needs.
2. **Editor UI** — a templates section beside `email-settings.tsx` / `theme-settings.tsx`, with a
   variable palette per kind, inline validation, and a live preview driven by the backend preview
   endpoint so preview and delivery cannot drift.
3. **Per-org and per-recipient locale** — the two earlier arguments to `ResolveLocale`.
4. **The logo in the mail header.** Open decision, deliberately not invented here. The header
   currently renders the org name as a text wordmark. `GET /orgs/{slug}/theme/logo` is
   member-gated and a mail recipient is by definition not a member, so a hotlink would resolve to
   403. The two options:
   - a publicly fetchable, versioned logo path (no membership check, cacheable) — also fixes the
     pre-auth branding gap where `/login` can only replay a cached palette, but it is an
     authorisation change and therefore a maintainer's call;
   - embed the logo as a MIME `cid:` part — no new public surface and it renders offline, but it
     needs `mailer.Message` to grow an inline-attachment part (multipart/related), which the
     issue lists as out of scope for the transport.

## 8. Files

| file | holds |
|---|---|
| `catalog.go` | kinds, their variable allowlists, locales, `Template`, embedded defaults |
| `templates/defaults.{en,nl}.json` | the shipped copy, per locale |
| `render.go` | validation, placeholder substitution, escaping, URL checks |
| `shell.go` | the mail-client-safe branded layout, both parts |
| `brand.go` | `org_theme_settings` seeds → an AA-guaranteed mail palette |
| `service.go` | resolves SMTP config + locale + template + brand, then sends |
| `cmd/api/main.go` | `mailBranding`, the adapter from the theming slice to `brandSource` |
