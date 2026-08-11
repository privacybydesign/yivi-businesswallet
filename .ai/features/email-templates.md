# Transactional mail templates and the branded mail shell

**Status:** rendering layer, tenant editing, the block-based WYSIWYG designer and the uploaded
logo image in mail implemented (#132). Per-org/per-recipient locale is still open; see §9.
**Slice:** `internal/email` composition and the per-org template overrides, plus the editor UI.
The SMTP transport (`internal/mailer`) grew inline-image (multipart/related) support for the logo;
the per-org SMTP settings model is untouched.

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
             ├─ Store.ResolveTemplate           templatestore.go  the org's edit,
             │    └─ DefaultTemplate(kind, locale)  catalog.go     else shipped copy
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
| `event_notification` | the notification layer's e-mail channel (`internal/emailchannel`) | `orgName`, `eventName`, `eventDetails`, `eventTime`, `auditUrl` |
| `postguard_file` | PostGuard "own SMTP" delivery | `orgName`, `message`, `downloadUrl` |
| `smtp_test` | `POST /email/test` | `orgName` |

`event_notification` is the one kind whose recipients are the organization's own admins rather than
an outside person, and the one whose `eventName` is itself catalogue copy: `templates/defaults.<locale>.json`
carries an `events` map from audit action to its name in that language, wording matched to the audit
log's own labels, and `EventLabel` resolves it. Every locale names the same set of actions (checked at
init) and that set is exactly the notification catalog (checked in `internal/emailchannel`). `eventDetails`
is the only variable that may resolve to empty: its paragraph then drops out of the layout, as any
all-empty text block does.

The variable list is what a caller actually passes today. The issue also lists `inviterName` for
the invitation; `organization.Handler` does not carry the inviter into the send, so declaring it
would mean a template could reference a value that is never supplied. It gets added together with
the caller change, not before.

The shipped copy lives in `templates/defaults.<locale>.json`, `go:embed`ed and loaded once at
package init. A missing kind, an unknown kind, an undeclared placeholder or an empty shell string
panics at init: the files are committed data, so any of those is a build mistake, and every test
run in the package initialises it.

## 3. Templates are block layouts, not HTML

A `Template` is `subject`, `preheader` and `blocks`: an ordered list of typed blocks — `logo`,
`heading`, `paragraph`, `button`, `divider`, `footer` — that a tenant adds, reorders and removes
(#132's WYSIWYG designer). Which fields a block carries depends on its type (`text` for
heading/paragraph/footer, `label`/`url`/`linkFallback` for button, nothing for logo/divider), and
a field on the wrong type is a validation error, not something to drop at render time. There is
deliberately **no HTML block and no HTML body field**.

That is the central design decision, and it settles the question raised on #132 about authoring
the shell with react-email / MJML / Maizzle and compiling it to Go:

- **A tenant never authors HTML.** The block set is closed and every type renders through the
  same fixed, mail-client-safe markup, so no layout a tenant can compose breaks in an inbox, and
  there is no HTML sanitiser to get wrong.
- **The `text/plain` alternative is generated, not required.** Both parts come from the same
  resolved block layout, so they cannot say different things and a tenant cannot leave one
  behind. Only the divider has no text rendering — it is decoration.
- **No Node in the build.** The layout is one hand-written Go function per block type
  (`shell.go`), pinned by tests, rather than generated output that a CI drift check has to keep
  honest. A compile step buys component ergonomics for markup a tenant cannot touch and nobody
  edits per release; the cost is a second toolchain plus a drift guard. If the shell ever grows
  past what one function can carry, revisit — the tenant-facing contract (typed blocks) does not
  change if it does.

Layout guardrails: at most 24 blocks (`maxBlocks`), and at least one heading or paragraph — a
message of only decoration says nothing. The preheader falls back to the layout's first heading,
else its first paragraph.

Rendering is not `text/template` execution. Placeholders are `{{name}}`, resolved against the
kind's declared variables and nothing else:

- an undeclared placeholder, or a malformed one like `{{ org name }}`, is a validation error at
  save time and again at render time, never a silent blank;
- values are escaped per part (HTML-escaped in the HTML alternative, raw in the text one), and
  the block prose is escaped the same way, so tenant text cannot introduce markup;
- URL variables must parse as absolute `http(s)` URLs with a host;
- a button's `url` is the one field that lands in an `href`, so it is a closed shape: either a
  single declared URL variable (`{{claimUrl}}`, whose value gets the check above) or an absolute
  `http(s)` literal with no placeholders. A literal with a placeholder spliced into it, or a
  non-URL variable, is a save-time error — neither can be checked without rendering. The resolved
  URL is checked once more before the shell writes it. Together that is what makes a relative,
  `javascript:` or `data:` call to action unreachable, whether the tenant wrote a variable or a
  literal;
- the subject is collapsed to a single line before `internal/mailer`'s header sanitising sees it;
- a text block whose every placeholder resolves to an empty value is dropped whole, which is how
  an optional part disappears instead of leaving a dangling label ("Your wallet will ask for this
  code: " with no code). The button is the exception: a `label` that collapses loses the button,
  but the bare URL stays, because a message with no way to act on it is worse than a message with
  an unlabelled link.

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

## 7. Tenant editing

`org_email_templates` holds one row per `(organization, kind, locale)` an org has **actually
edited**: the subject, the preheader, and the block layout as one `blocks` JSONB column (the
fixed prose columns of the first iteration were converted in place by the
`email_templates_block_layout` migration). There is no row for a cause a tenant has not touched,
which is the whole design:

- a send resolves through `Store.ResolveTemplate` — the org's row when it exists, the shipped
  default otherwise — so improving `templates/defaults.<locale>.json` still reaches every org
  that has not customised that cause;
- **reverting is a `DELETE`**, not a write of the current default. Writing the default back would
  freeze today's wording into the tenant's row and quietly opt them out of every later
  improvement;
- each `(kind, locale)` is independent: an org can rewrite the Dutch invitation and keep the
  English one shipped.

`SaveTemplate` calls `ValidateTemplate` before it writes, so the rules of §3 are save-time rules,
not send-time surprises. A refusal comes back as `email.InvalidTemplateError`, which carries the
reason separately from the wrapping prose so the handler can answer `400 invalid_template` with
the field named — that string is what the editor shows beside the input.

A template lookup that fails logs and falls back to the shipped copy, for the same reason a failed
brand lookup does (§4): mail in the default wording still delivers, mail not sent breaks the flow
it belongs to.

### Routes (org-admin, beside the SMTP settings)

| route | does |
|---|---|
| `GET /orgs/{slug}/email/templates` | the whole kind × locale matrix, each cell marked customised or default, plus each kind's variable allowlist |
| `GET|PUT|DELETE /orgs/{slug}/email/templates/{kind}/{locale}` | read / save / revert one cell; every response carries both the template in force and the shipped default |
| `POST /orgs/{slug}/email/templates/{kind}/preview` | render a draft (or what is in force) with sample variables and the org's branding, returning the subject and both MIME parts |
| `POST /orgs/{slug}/email/test` | now takes an optional `kind` + `locale`, so an admin can send a real specimen of one cause instead of only the SMTP self-test |

Kinds and locales are closed backend sets, so a value outside them is a **404** (a request for
something that does not exist), not a 400. Audit: `email.template_updated` /
`email.template_reset` against `org_email_template`, with the target id `kind/locale` — the row's
UUID changes on a revert-then-edit, whereas `(kind, locale)` is what an admin reading the log
recognises.

### Sample values

A preview and a specimen send need stand-ins for every variable except `orgName`, which is the
real organization name (a preview that renamed the tenant would not be showing what a recipient
gets). Those stand-ins are copy, so they live in `templates/defaults.<locale>.json` under
`samples` and are checked at package init: a variable with no sample, an empty one, or a URL
sample that is not absolute `http(s)` panics at init, and a sample for a variable no kind declares
is rejected as a leftover.

## 8. The designer UI

`frontend/src/routes/email-templates.tsx`, a section of the Settings page's **Communication** tab
beside e-mail, Slack, Microsoft Teams and notifications (`frontend/src/lib/settings-tabs.ts`).
It lists the causes for one language at a time, with a customised/default badge, and opens one
template at a time in a block designer: the tenant adds, reorders and removes blocks, and edits
each block's fields in place.

- **The preview is the backend's.** `POST .../preview` returns the same HTML a recipient gets, and
  the editor drops it into a fully sandboxed `<iframe srcDoc>` (`sandbox=""`, so no script and no
  navigation). The alternative — rendering the layout again in React — would be a second shell to
  keep in step with `shell.go`, and it would be the one nobody tests against a real inbox.
- **Reordering is a pair of move buttons, not drag and drop**, so it works with a keyboard and a
  screen reader. Draft blocks carry a stable client-side key (`DraftBlock`), because an
  index-keyed list would leave focus on the wrong block after a move.
- **`frontend/src/lib/mail-template.ts` mirrors the validation rules** so a tenant sees the
  problem beside the field they are typing in. It is a convenience, not the gate: the backend's
  400 is surfaced verbatim, and the mirror never rejects anything the backend would accept. The
  two have to be kept in step — `mail-template.test.ts` pins the mirror's half.
- The variable palette offers exactly the kind's declared variables and inserts at the caret of
  the last focused field, so nobody has to type the brace syntax or guess what is available.
- Saving is disabled while a draft has problems or is unchanged; reverting is behind a
  `ConfirmDialog` because the tenant's version is not kept.

## 9. Not built yet

The remainder of #132:

1. **Per-org and per-recipient locale** — the two earlier arguments to `ResolveLocale`. The editor
   already keys templates per locale, so this is only about *choosing* the locale for a send.

The uploaded logo image in mail is now **built** (the `cid:` route): the `logo` block embeds the
org's uploaded logo as a `multipart/related` inline part (`Content-ID: <orglogo>`), so it renders in
clients that block remote images and offline, with no new public surface. `mailer.Message` grew an
`Inline []InlineImage` for this. The block still falls back to the org name as a text wordmark when
no logo is set, and the org name is the image's `alt` text so an images-off client shows the sender.
The editor preview cannot resolve `cid:` inside its sandboxed iframe, so `Service.Preview` swaps the
reference for a `data:` URI (`inlinePreviewLogo`) — the same bytes, shown the way a frame can render
them, so preview and delivery still show the same image. The logo image is only fetched for a layout
that actually has a logo block (`brandWithLogo` / `templateHasLogoBlock`). A publicly fetchable logo
path (which would also fix the pre-auth `/login` branding gap) was the alternative and remains a
possible future authorisation change, but is not needed for mail.

## 10. Files

| file | holds |
|---|---|
| `catalog.go` | kinds, their variable allowlists, locales, `Template` + `Block`, embedded defaults and samples |
| `templates/defaults.{en,nl}.json` | the shipped default block layouts, per locale |
| `render.go` | validation (template + per block), placeholder substitution, escaping, URL checks |
| `shell.go` | the mail-client-safe branded rendering of every block type, both parts |
| `brand.go` | `org_theme_settings` seeds → an AA-guaranteed mail palette |
| `service.go` | resolves SMTP config + locale + template + brand, then sends; `Preview`, `SendSpecimen`, `SendEventNotification` |
| `internal/emailchannel/` | the notification layer's e-mail channel: recipients, the audit-log link, and how much metadata a notification repeats |
| `templatestore.go` | the per-org overrides: list / get / save / revert, and `ResolveTemplate` |
| `templatehandler.go` | the tenant-editing routes and their request/response shapes |
| `cmd/api/main.go` | `mailBranding`, the adapter from the theming slice to `brandSource` |
| `frontend/src/routes/email-templates.tsx` | the editor tab |
| `frontend/src/lib/mail-template.ts` | the client-side mirror of the validation rules |
