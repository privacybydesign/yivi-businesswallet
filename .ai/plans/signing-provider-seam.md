# Signing-provider seam: methods, types, authorization shape and layering

Status: **proposed** (design only — no production code). Issue [#175](https://github.com/privacybydesign/yivi-businesswallet/issues/175), a grilling ticket under the qualified signing & sealing design map [#171](https://github.com/privacybydesign/yivi-businesswallet/issues/171).

Feeds on the three closed research tickets: [#172](https://github.com/privacybydesign/yivi-businesswallet/issues/172) (CSC API + NL QTSP landscape), [#173](https://github.com/privacybydesign/yivi-businesswallet/issues/173) (AdES formats + Go tooling), [#174](https://github.com/privacybydesign/yivi-businesswallet/issues/174) (eIDAS-2 sole-control direction).

## Why

Art 5(1)(d) of COM(2025) 838 requires the wallet to create qualified electronic signatures and seals. We are not the QTSP: signing happens on a QTSP-hosted QSCD and we are the requestor, the same relationship `internal/qerdsprovider` already has with a QERDS provider and `internal/irmarequestor` has with the Yivi daemon. So the capability arrives as one more external-provider seam, and the backend convention already fixes its shape (`.ai/conventions/BACKEND.md`, "External-provider seam"): a thin typed client behind a consumer-defined interface, chosen by config, in-process stub as the dev/CI default, fatal boot `Ping`.

What is *not* already fixed is the part this document locks, and the research makes each of these a real question rather than a copy of `qerdsprovider`:

1. **Sign and seal do not share a driver path.** #172 found NL QTSPs gate seals behind proprietary non-CSC APIs (Digidentity's eSeal Autosigner is `POST /v1/auto_signers/{id}/sign`, not CSC). A seam that assumes one protocol for both is wrong on the first real provider.
2. **Signing is a two-step interactive flow**, not one call. SCAL2 binds document hashes at authorization time and returns signature activation data (SAD) that authorizes exactly those hashes. `qerdsprovider.Send` has no analogue for that.
3. **The document must not cross the seam.** #173 found the QTSP signs only the hash and the client owns all PAdES construction. Passing bytes to the driver would put document handling in the wrong layer and couple the seam to the still-open format decision (#177).

Everything else in the map hangs off the answer, so this is the first ticket on the frontier.

## Where it sits

Two packages, the same split as `qerdsprovider` / `qerds`, and the same top-down layering (`.ai/conventions/BACKEND.md`, Layering):

```
cmd/api/main.go                    newSigningProvider(cfg) switch + fatal boot Ping
        │ injects the concrete driver
        ▼
internal/signing                   domain slice, org-scoped
  handler.go       auth.RequireUser → organization.Authorize → RequirePermission(signing, …)
  service.go       orchestrates stores + provider + audit (2+ collaborators, so a service)
  request_store.go signing requests
  document_store.go input + produced bytes, content-opaque
  evidence_store.go append-only
  status.go        signing state machine
  signing.go       domain types + sentinel errors
        │ depends on a consumer-defined `provider` interface
        ▼
internal/signingprovider           no domain logic
  provider.go      value types (below)
  authenticator.go RequestAuthenticator, same body-level seam as qerdsprovider
  stub.go          StubProvider — in-process, dev/CI default
  csc.go           CSCProvider — net/http driver, hand-written (no Go SDK exists, #172)
```

Three layering rules that matter more here than they did for QERDS:

- **`signingprovider` imports no other `internal/` package.** It is leaf-level, like `qerdsprovider`. It does not know about orgs, users, or the RBAC model.
- **`signing` does not import `qerds`, and `qerds` does not import `signing`.** Sign-then-deliver is wired at the top in `main.go` through a consumer interface on the `signing` side, exactly how `attestation` hooks into inbound QERDS today via `qerds.Service.SetInboundConsumer`. That keeps the map's sign-and-deliver fog out of both slices' import graphs.
- **The `Provider` interface below is documentation.** Per "accept interfaces, return structs", `signingprovider` exports value types and concrete constructors only; the compiled interfaces are the narrow consumer-defined ones in `internal/signing` (`provider`) and `cmd/api/main.go` (`signingProvider`, which adds `Ping`). `qerdsprovider` does the same and declares no *provider* interface of its own, its one exported interface being `RequestAuthenticator`, the body-level auth seam this package copies.

## The seam contract

```go
type Provider interface {
	Ping(ctx context.Context) error
	Capabilities() Capabilities
	Credentials(ctx context.Context, subject SubjectRef) ([]Credential, error)
	BeginAuthorization(ctx context.Context, req AuthorizationRequest) (Authorization, error)
	FinishAuthorization(ctx context.Context, cb AuthorizationCallback) (Authorization, error)
	Sign(ctx context.Context, req SignRequest) (SignReceipt, error)
	Timestamp(ctx context.Context, req TimestampRequest) (Timestamp, error)
}
```

Seven methods, mapping onto CSC v2 as `info` → `credentials/list` + `credentials/info` → `credentials/authorize` (twice) → `signatures/signHash` → `signatures/timestamp`. Every method must honour `ctx`: a `context.WithTimeout` around a driver call bounds nothing unless the callee selects on `ctx.Done()`, and the stub is a callee too.

### Sign and seal are the same methods over a different credential

**Decision: one method set, and the sign/seal difference lives in the credential, not in the signature of any method.**

```go
type CredentialKind string

const (
	CredentialSignature CredentialKind = "signature" // QES — a natural person signs
	CredentialSeal      CredentialKind = "seal"      // QESeal — the organisation seals
)

// SCALLevel is the sole-control assurance level of a credential (EN 419241-1).
type SCALLevel string

const (
	SCAL1 SCALLevel = "1" // key usable without a fresh per-signature authorization
	SCAL2 SCALLevel = "2" // each signature needs a fresh authorization binding the hashes
)

// AuthMode is how a credential's authorization leg is driven. It is the branch
// point for BeginAuthorization / FinishAuthorization, read off the credential.
type AuthMode string

const (
	AuthExplicit   AuthMode = "explicit"   // we collect an OTP/PIN and submit it ourselves
	AuthOAuth2Code AuthMode = "oauth2code" // the signer authenticates at the QTSP, returns a code
	AuthImplicit   AuthMode = "implicit"   // machine-held, granted without an interactive leg
)

// SubjectRef identifies whose credentials to list.
type SubjectRef struct {
	Kind      CredentialKind
	SubjectID string
}

// Credential is one signing or sealing credential the subject may use.
type Credential struct {
	ID          string
	Kind        CredentialKind
	Label       string
	SCAL        SCALLevel // "1" or "2" (EN 419241-1)
	AuthMode    AuthMode  // explicit | oauth2code | implicit
	MultiSign   int       // how many hashes one authorization covers
	Qualified   bool      // the QTSP asserts this credential is qualified
	Certificate [][]byte  // signing cert + chain, DER
	ValidFrom   time.Time
	ValidUntil  time.Time
	HashAlgos   []string // OIDs the credential accepts
	SignAlgos   []string // OIDs the credential can produce
}
```

What differs between a signature and a seal is then data on the credential, not a separate code path in the caller:

| | QES (signature) | QESeal (seal) |
|---|---|---|
| `Kind` | `signature` | `seal` |
| Who the `SubjectRef` names | the signing natural person | the organisation |
| Typical `SCAL` / `AuthMode` | `2` / `explicit` or `oauth2code` — the signer must act | `1` / `implicit` — machine-held, no interactive leg |
| Driver path | CSC `credentials/authorize` + `signatures/signHash` | may be a proprietary autosigner endpoint (#172) |

The last row is the reason this shape was chosen over a second `Sealer` interface. A driver is free to serve `Kind: seal` over a completely different protocol; the domain slice never learns which, because it only ever sees `Credential` and `SignReceipt`. That absorbs #172's finding without a second seam.

**A caller must not branch on `Kind` to decide whether to authorize.** It branches on `Authorization.Status`, which is `granted` immediately for an `implicit` credential. Branching on `Kind` would break the moment a QTSP ships a SCAL2 seal (which eIDAS 2 pushes toward) or a SCAL1 machine signature.

### The document never crosses the seam

**Decision: the seam takes data-to-be-signed representations (DTBS/R), never document bytes.**

```go
// DocumentDigest is one data-to-be-signed representation (DTBS/R).
type DocumentDigest struct {
	ID            string // caller-minted, opaque and unique within one request — the correlation key
	Name          string // human label; reaches the QTSP's consent screen only
	Hash          []byte // raw digest bytes — the driver does the base64/hex
	HashAlgorithm string // OID, e.g. 2.16.840.1.101.3.4.2.1 for SHA-256
}
```

This follows #173 directly: the QTSP signs the hash and returns raw signature bytes, and the client owns the ByteRange placeholder, the CMS `SignedData` and the timestamp embedding. What that buys, and why the seam should survive the format decision:

- **The seam is format-agnostic.** PAdES, XAdES, CAdES and JAdES all reduce to a hash plus a signature value, so the #177 format decision changes the PAdES builder in `internal/signing`, not this interface.
- **`Hash` is raw bytes, not an encoded string.** Encoding is the wire format's business, and CSC's base64 differs from what a proprietary autosigner wants. A pre-encoded string at the seam would force every caller to know the driver.
- **Document storage stays in the domain slice**, reusing the content-opaque blob-column pattern from `qerds_attachments` (hash + size as integrity metadata, bytes in a BYTEA column, `storage_ref` reserved).

`Name` is the one field that leaves our system as text. It is a filename shown on the QTSP's consent screen, so the handler bounds and sanitises it like any other user-supplied filename.

### authorize → sign (SCAL2)

**Decision: two methods, `BeginAuthorization` and `FinishAuthorization`, both returning the same `Authorization` value.**

```go
// AuthorizationRequest binds a set of digests to a credential (SCAL2).
type AuthorizationRequest struct {
	CredentialID string
	Digests      []DocumentDigest
	RedirectURL  string // where the QTSP returns the signer, for oauth2code
	State        string // our CSRF/correlation value for that redirect
	Reason       string // signing reason shown to the signer
}

// Authorization is the outcome of an authorization attempt.
type Authorization struct {
	ProviderRef    string     // the QTSP's handle for this authorization
	Status         string     // pending | granted | rejected | expired
	SignerRedirect string     // set when the signer must act at the QTSP
	Activation     Activation // set when Status is granted
	ExpiresAt      time.Time
	FailureCode    string // closed set, see below
}

// AuthorizationCallback resumes an authorization begun earlier.
type AuthorizationCallback struct {
	ProviderRef  string
	CredentialID string           // re-supplied from our persisted row, so Finish needs no driver-side state
	Digests      []DocumentDigest // the digests bound at Begin; CSC explicit sends them with the OTP
	Code         string           // oauth2code: the authorization code
	State        string           // oauth2code: echoed back for verification
	Factor       string           // explicit: the OTP/PIN the signer supplied
}

// Authorization.Status values. This is the branch point of the whole design, so
// the closed set is declared, not left in a comment (cf. qerdsprovider).
const (
	AuthStatusPending  = "pending"  // an interactive leg is still outstanding
	AuthStatusGranted  = "granted"  // Activation is set; ready to Sign
	AuthStatusRejected = "rejected" // the signer declined or a factor failed terminally
	AuthStatusExpired  = "expired"  // the window closed before the signer acted
)
```

One `AuthorizationCallback` covers all three CSC auth modes because each mode needs a different subset of it, and a caller that already read `Credential.AuthMode` knows which:

| `AuthMode` | `BeginAuthorization` returns | `FinishAuthorization` needs |
|---|---|---|
| `explicit` | `pending`, no redirect — we collect the OTP/PIN ourselves | `ProviderRef` + `Factor` |
| `oauth2code` | `pending` + `SignerRedirect` — the signer authenticates at the QTSP | `ProviderRef` + `Code` + `State` |
| `implicit` | `granted` directly, or `pending` while the QTSP pushes to the signer | `ProviderRef` only; call again to poll |

So `FinishAuthorization` doubles as the poll for `implicit`, rather than a third method that exists only for one mode. A `pending` result on a `Finish` call is normal and not an error.

Why not one `Authorize` returning a union: the two legs run in **different HTTP requests of ours**, and on a horizontally-scaled deployment those two requests can land on different replicas or straddle a restart. `Begin` runs in the request that starts the ceremony; `Finish` runs in the OAuth2 callback or the OTP-submit request, which have no shared memory with the first — an in-process map would be empty on the second pod. That is why `AuthorizationCallback` carries `CredentialID` and `Digests`, not just `ProviderRef`: the domain layer reloads them from the persisted `signing_requests` row and re-supplies them, so the driver's `Finish` holds no state between the two requests. Two methods make that boundary explicit, and it is where the persistence rule below bites.

**`ProviderRef` identifies the authorization, not the signature.** CSC's `signatures/signHash` is synchronous, so unlike QERDS delivery there is usually no long-lived remote handle for the *signature* — the ref that matters is the one minted at `BeginAuthorization` and made unique on `signing_requests.provider_ref`. `SignRequest` therefore carries that `ProviderRef` in, and `SignReceipt.ProviderRef` echoes it back out, so the receipt joins the row already persisted rather than arriving as a fresh unrelated value the driver has no way to recover (the SAD is the QTSP's opaque blob, not a lookup key). There is deliberately no `Status(ctx, providerRef)` polling method for signatures in v1; add one when a driver needs it (an async proprietary autosigner is the likely first). What the ref is *never* derived from is our own request id, which does not exist on the provider's side — the same correlate-by-provider-ref rule `qerds_messages.provider_ref` already follows.

### Activation data is request-scoped and never persisted

The SAD is a bearer secret that authorizes exactly the bound hashes for exactly `MultiSign` uses until `ExpiresAt`. The interface makes that hard to get wrong:

```go
// Activation carries signature activation data (SAD, EN 419241-1).
type Activation struct {
	sad string
}

func NewActivation(sad string) Activation { return Activation{sad: sad} }
func (a Activation) SAD() string          { return a.sad }
func (a Activation) Granted() bool        { return a.sad != "" }
func (a Activation) String() string       { return "[REDACTED]" }
func (a Activation) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }
```

The field is unexported, so `encoding/json` marshals `{}` and no store can round-trip it by accident. `String` covers `%v`, `%s`, `%+v` and `%q`; `LogValue` covers `slog.Any`, which is what the backend convention says to use. Measured on Go 1.26.4, both directly and nested one struct deep. **`%#v` is not covered** and cannot be — fmt's Go-syntax verb reads unexported fields directly and no method intercepts it. That residual is small (nothing in the backend formats with `%#v`) but it is real, so it is written down rather than claimed away. The easier leak is the exported `SAD()` accessor itself: `slog.Any("sad", auth.Activation.SAD())` or `fmt.Errorf("%s", a.SAD())` prints the secret with no unusual verb, and unlike `%#v` it will not look wrong in review. The accessor has to exist — the driver needs the SAD on the wire — so the invariant is a usage rule, not a type guarantee: **`SAD()` is for the driver's outbound wire call only**, never a log field and never an error string.

The invariant that goes with the type: **`Activation` never reaches a store or a log line.** Between `Begin` and `Finish` we persist only the `ProviderRef`, the `credential_id` and the digests (none of which is secret) — exactly what `AuthorizationCallback` re-supplies so `Finish` is stateless. `Finish` and `Sign` run in the same request, so the SAD lives in one call stack and dies with it. If a flow ever needs the SAD to outlive a request, that is a design change to argue for, not a field to add to a store.

`FailureCode` is a closed set for the same reason. A driver **maps** onto it and never repeats the responder's own words, because a passthrough string reaches both a log line and an API error body:

```go
const (
	FailureInvalidFactor       = "invalid_factor"
	FailureFactorExpired       = "factor_expired"
	FailureSignerDeclined      = "signer_declined"
	FailureCredentialLocked    = "credential_locked"
	FailureDigestNotAuthorized = "digest_not_authorized"
	FailureProviderUnavailable = "provider_unavailable"
)
```

`FailureCode` closing the mapped code is only half the surface: every method also returns `error`, and a driver's `error` must not carry the responder's own bytes either — a sentinel or a code-derived message, never a passthrough, since that error reaches the same log line and API body. A CSC driver has all four positions this repo has already leaked a secret or a URL through, and each is closed by name: the **response body** (an error document the responder wrote); the **status line** — Go keeps the responder's reason phrase verbatim in `resp.Status`, so build from `resp.StatusCode` + `http.StatusText`, never `resp.Status`; the **transport error** (`*url.Error` embeds the full request URL, so wrap it as a sentinel, don't format it into the message); and the **`encoding/json` decode error on a 200**, which carries the responder's first offending byte and sits on the success path where a fix reviewed against the other three never looks. This is the rule `slackchannel` (#159) and `mailoauth` (#168) each paid for; it is written here rather than as a fact about one package because any seam talking to somebody else's endpoint inherits it.

### SignReceipt and the evidence record

```go
// SignRequest asks the QSCD to sign the authorized digests.
type SignRequest struct {
	ProviderRef   string // the authorization from Begin/Finish, so the receipt can echo it
	CredentialID  string
	Digests       []DocumentDigest // must be the digests bound at Begin
	Activation    Activation
	SignAlgorithm string // OID from Credential.SignAlgos
}

// SignReceipt is what the provider returns for a completed signing operation.
type SignReceipt struct {
	ProviderRef string
	Status      string // signed | failed
	Signatures  []Signature
	Evidence    []Evidence
}

// Signature is one raw signature value plus the material needed to embed it.
type Signature struct {
	DigestID    string   // matches DocumentDigest.ID — the correlation key, unique per request
	Value       []byte   // raw signature bytes
	Algorithm   string   // signature algorithm OID
	Certificate [][]byte // signing cert + chain as used, DER
}

// Evidence is a single tamper-evident signing evidence record.
type Evidence struct {
	Type               string
	ProviderRef        string
	QualifiedTimestamp time.Time
	Raw                []byte
}

// SignReceipt.Status values.
const (
	SignStatusSigned = "signed"
	SignStatusFailed = "failed"
)

// Evidence.Type values. These land verbatim in signing_evidence.evidence_type,
// so the closed set is declared like qerdsprovider's Evidence* constants.
const (
	EvidenceAuthorization  = "authorization"   // the SAD grant: which hashes, which credential, when
	EvidenceSignature      = "signature"       // the signature-creation event at the QSCD
	EvidenceTimestamp      = "timestamp"       // the qualified RFC 3161 timestamp token
	EvidenceRevocationInfo = "revocation-info" // OCSP/CRL captured at signing time
)
```

`SignReceipt` is deliberately the `qerdsprovider.SendReceipt` shape (`ProviderRef` + `Status` + `Evidence`) with `Signatures` added, and `Evidence` is field-for-field the `qerdsprovider.Evidence` shape. That is not cosmetic: it lets `signing_evidence` be the same append-only table design as `qerds_evidence`, and it means the Art 5(1)(n) verify-and-export UI has one evidence vocabulary to render, not two.

`Signatures` correlates to the request by `DigestID` — a caller-minted, opaque, per-request-unique key — not by slice position and not by `Name`. Position is what a driver silently gets wrong when a provider reorders or drops one; `Name` is a human filename that is neither unique (a `MultiSign` authorization can hold two files both called `contract.pdf`) nor stable (sanitisation collapses `a/b.pdf` and `a_b.pdf` to one string), so correlating on it embeds the wrong signature in the wrong document while passing every set-equality check. `DigestID` is unique by construction, so the caller must reject a receipt whose `DigestID` set is not exactly the requested set. `Name` stays display-only.

Evidence types, and why each one has to be able to come back through the seam:

| `Type` | What it is | Why the seam must carry it |
|---|---|---|
| `authorization` | the SAD grant: which hashes, which credential, when | the sole-control audit trail (#176) — proof the signer authorized *these* bytes |
| `signature` | the signature-creation event at the QSCD | the Art 5(1)(m) transaction log entry |
| `timestamp` | the qualified timestamp token (RFC 3161) | PAdES **B-T** cannot be built without it (#173) |
| `revocation-info` | OCSP/CRL captured at signing time | PAdES **B-LT** cannot be built without it (#173) |

#173 concluded B-LT/B-LTA needs a DSS service boundary, but it needs the *material* first, and signing time is the only moment revocation data is guaranteed fresh for the certificate actually used. A seam that returned only the signature value would make climbing from B to B-T to B-LT an interface change every step. `Timestamp` is a separate method for the same reason: #172 found Digidentity reuses a CSC endpoint for timestamping even on its non-CSC seal path, so a provider can offer timestamping independently of how it signs.

```go
// TimestampRequest asks for a qualified RFC 3161 timestamp over one hash.
type TimestampRequest struct {
	Hash          []byte // the digest to timestamp — raw bytes, driver encodes
	HashAlgorithm string // OID of the digest algorithm
}

// Timestamp is a qualified RFC 3161 timestamp token, the material PAdES B-T needs.
type Timestamp struct {
	Token       []byte    // the DER-encoded TimeStampToken (RFC 3161)
	GeneratedAt time.Time // the genTime the TSA asserts, for the evidence record
}
```

The token is the same bytes a `timestamp` evidence record carries; `Timestamp` is offered separately so a caller can timestamp a hash the seam did not sign (an archival re-timestamp toward B-LTA, #173), which is why it takes a bare hash rather than a `SignReceipt`.

### Capabilities, config swap and Ping

```go
// Capabilities is what a driver can do with its current configuration. It is a
// static property of driver plus config, not a remote call.
type Capabilities struct {
	Signing      bool
	Sealing      bool
	Timestamping bool
}
```

**Decision: `Capabilities()` is synchronous and takes no context.** It answers "was this deployment configured with a seal path at all", which is a property of the driver and its config, and the handler needs it while rendering a screen. What varies at runtime (a credential's SCAL level, its auth mode, its validity window) is per-credential and comes from `Credentials()`, which is a remote call. Splitting it that way keeps `Capabilities` from becoming a reason for every screen to wait on the QTSP.

This is what #172 forces us to have: `Capabilities().Sealing == false` is the honest state for a QTSP whose seal API we have not driven yet, and the UI must not offer a seal button that 501s.

`Ping` is the CSC `info` endpoint, which needs no per-signer authorization, so it probes exactly what the QERDS `Ping` does: credentials valid, endpoint reachable, our client provisioned. **Fatal at boot**, same as every other provider in `main.go`; `/readyz` stays DB-only.

Config follows the existing naming (`internal/config`, `QERDS_*` / `ATTESTATION_*` precedent):

| Var | Meaning | Required |
|---|---|---|
| `SIGNING_PROVIDER` | `stub` (default) or a driver name (`csc`) | no |
| `SIGNING_PROVIDER_URL` | CSC base URL | when not `stub` |
| `SIGNING_OAUTH_CLIENT_ID` | CSC OAuth2 client id | when the driver needs it |
| `SIGNING_OAUTH_CLIENT_SECRET` | CSC OAuth2 client secret | when the driver needs it |
| `SIGNING_OAUTH_REDIRECT_URL` | our callback for the `oauth2code` leg | when the driver needs it |
| `SIGNING_STUB_FACTOR` | the OTP the stub accepts in dev | no (has default) |

Per-org provider config (one tenant on Cleverbase, another on Digidentity) is out of scope here and belongs to the map's sign-and-deliver ticket. Nothing in the interface blocks it: it becomes which driver instance the service resolves per org, not a new method.

## What `internal/signing` owns

The domain slice, not the seam. Sketched only far enough to show the seam is sufficient.

- **`signing_requests`** — `(id, org_id FK, created_by_user_id FK, credential_kind, credential_id, credential_label, format, status, provider_ref, authorization_expires_at, created_at, updated_at)`. `provider_ref` unique.
- **`signing_documents`** — `(id, request_id FK, role ['input'|'signed'], filename, content_type, content_hash, size_bytes, content, storage_ref)`. Content-opaque, same MVP blob-column shape as `qerds_attachments`.
- **`signing_evidence`** — append-only, `(id, request_id FK, evidence_type, provider_ref, qualified_timestamp, raw_evidence, created_at)`. Never updated.

State machine (`status.go`): `draft → awaiting_authorization → authorized → signed`, terminal `failed` / `expired`. Transitions are idempotent and keyed on `provider_ref`, because the `oauth2code` callback can be replayed by a refresh.

Authorization, in the vocabulary `.ai/plans/rbac-model.md` fixes: a new resource `signing` with actions `read`, `sign`, `seal`, `manage_credentials`. One invariant that belongs here rather than in the RBAC doc, because only the seam makes it visible:

> **The subject of a QES authorization is the session user, never a request parameter.** A signature is a natural person's act. `SubjectRef.SubjectID` for `Kind: signature` is derived from the authenticated user, so no permission and no request body can make user A authorize a signature under user B's credential. A **seal** is the opposite: the subject is the organisation, so it *is* permission-gated (`signing:seal`), and that permission is an administrative-mandate capability.

## Decisions settled

Against the four bullets the ticket asked for:

1. **Methods and types for signing and sealing, and how the two differ.** Seven methods (above), one set for both. The difference is `Credential.Kind` plus who `SubjectRef` names plus whether an interactive leg exists — all data, no branching in the caller, and a driver may serve sealing over a non-CSC protocol invisibly.
2. **The authorize → sign (SCAL2) shape.** `BeginAuthorization` / `FinishAuthorization` returning one `Authorization`, with `AuthorizationCallback` covering all three CSC auth modes and `Finish` doubling as the `implicit` poll. Activation data is request-scoped, unexported, redacted under `String`/`LogValue`, and never persisted.
3. **`SignReceipt` / evidence shape.** `SendReceipt`-shaped, `Signatures` correlated by `DigestName`, and four evidence types chosen so the B → B-T → B-LT ladder (#173) needs no interface change.
4. **Config-swap, `Ping`, correlate-by-provider-ref, and the dependency hierarchy.** `SIGNING_PROVIDER` switch in `main.go` with `stub` as the dev/CI default and a fatal boot `Ping` on CSC `info`; `Capabilities()` static and context-free so a missing seal path is visible without a round trip; `ProviderRef` always provider-minted and identifying the *authorization*; `handler → service → store/client` with `signingprovider` leaf-level and no import edge between `signing` and `qerds`.

## The stub

`StubProvider` mints a self-signed P-256 key and certificate at construction and really signs, so the whole ceremony runs offline and the produced signature verifies against the credential certificate it hands back. That matters for #177: the PAdES embedding work gets something real to embed before any QTSP contract exists. It serves one `signature` credential (SCAL2, `explicit`, a configured dev OTP) and one `seal` credential (SCAL1, `implicit`, granted immediately), so both branches of the authorization table are exercised in CI.

Two details carried over from what `qerdsprovider.StubProvider` got wrong first:

- **Provider refs are random, never content- or counter-derived.** The stub's maps reset on restart but `signing_requests.provider_ref` persists and is unique, so a derived ref collides with a previous run's row — the same failure that left QERDS messages stuck in `submitted` (see the comment on `qerdsprovider.StubProvider.ref`).
- **A rejected `Finish` does not consume the pending authorization.** A wrong OTP is a retry, not a lost ceremony. A stub that deleted the pending entry on the first bad factor would make the retry path untestable and hide a real driver bug.

Same caveat as QERDS, in the same words: this proves plumbing, **not** compliance. A green stub loop must not read as "signing done" — qualified signatures, qualified timestamps and the certificate chain are only truly exercised against a QTSP sandbox.

## Deferred

Named so the next tickets do not have to rediscover the boundary:

- **PAdES construction, embedding and the DSS service boundary** — #177. This seam gives it a signature value, a chain, a timestamp token and revocation info; what it builds from those is that ticket's call.
- **Inbound signature validation** — #179. Verification is not a signing-provider concern; the driver signs, it does not judge.
- **Sole-control model details** — #176. This seam carries `SCAL`, `AuthMode` and the `authorization` evidence record; which model we require of a QTSP, and how the OpenID4VP layer supplies document-bound consent, is that ticket's. #174's recommendation (QTSP-owned sole control for v1, keep the seam abstract for the wallet-as-factor upgrade) is what this interface is shaped for: that upgrade is a new driver, not a new interface.
- **Certificate lifecycle and on-the-fly credentials** — #178. `Credentials()` lists what exists; provisioning is not on this seam.
- **Multi-party signing packages** — map fog. A package is N signing requests plus ordering and routing, one layer above `signing.Service`.
- **Per-org provider config**, and **an async signature `Status` method** for a proprietary autosigner that needs one.

## Verification

Doc-only; no build or test surface is affected. The Go in this document was not written blind — the full type set, a `StubProvider` implementing it, and a scratch `internal/signing` consumer interface were compiled out-of-tree against Go 1.26.4 with `gofmt` and `go vet` clean, and exercised by five tests: the explicit authorize→sign round trip (including a wrong-factor rejection that does not consume the pending authorization, and an `ecdsa.VerifyASN1` check that the stub signature verifies against the returned certificate), the implicit seal path granting with no redirect, `Sign` refusing a request with no activation data, the service reading `Capabilities().Sealing` through its consumer interface, and the `Activation` redaction probe across `%v`/`%s`/`%+v`/`%q`, `slog` and `encoding/json`. That probe is what turned up the `%#v` residual documented above. None of that code is in this PR.

## Harvest

- Convention to add or update in `.ai/conventions/<area>.md`? **none.** The seam deliberately instantiates the existing "External-provider seam (uniform shape)" convention in `BACKEND.md` rather than adding to it. The one new general lesson (a secret crossing a seam gets an unexported field plus `String` + `LogValue`, and `%#v` still leaks) is worth a convention line only if a second seam needs it.
- Feature doc to write or update in `.ai/features/<name>.md`? **`.ai/features/signing.md`** — but written by the map's capstone ticket, which folds #175 through #179 into one architecture doc. Writing it now would mean five rewrites. This plan is the interim source of truth for the seam.
