# Directory provisioning: users and departments from an identity source

**Status:** implemented, backend only. Microsoft Entra ID is the one source; there is no settings
screen yet, so an organisation is configured through `PUT /api/v1/orgs/{slug}/provisioning/settings`.
**Code:** `backend/internal/provisioner` (the seam + the Entra driver), `backend/internal/provisioning`
(configuration, reconciler, scheduler, HTTP).

---

## 1. What this is

Onboarding every member by hand does not scale past a small organisation, and HR/IT already keep the
authoritative list of who works there, in which department, and who has left. Directory provisioning
reads that list and mirrors it into the organisation's departments and memberships, on a schedule.

Three things are provisioned:

- **Departments**: the departments the source's people belong to become `departments` rows.
- **Users**: a person in the source becomes an invitation, and once they accept, a `membership`
  with the role, job title and department the source gave them.
- **Leavers**: someone removed or disabled in the source loses their pending invitation or their
  membership, without anybody here doing anything.

## 2. The seam

`provisioner.Provisioner` is one method:

```go
type Provisioner interface {
	ID() SourceID
	Fetch(ctx context.Context, cfg Config) (Directory, error)
}
```

It is read-only and stateless on purpose. Everything that decides what a snapshot *means* (which
department to create, who to invite, whose membership to take away, what to do about a conflict)
lives in `provisioning.Service`, so a second source (Google Workspace, Okta, a CSV upload) only has
to implement `Fetch`. It mirrors `internal/qerdsprovider` and `internal/registryprovider`: our
backend is a client of somebody else's system and the driver is swapped by configuration.

The one difference from those two: the source is configured **per organisation**, not per
deployment. A tenant brings its own directory, so there is no boot-time `Ping` gate: a tenant with
an expired secret must not fail everybody else's deploy. The outcome of a run lands on the
organisation's settings row (`lastRunStatus` / `lastRunError`) and in its audit log instead.

`Fetch` is waited on, not called straight through (`Service.fetchDirectory`). A `context` deadline
is a request the callee has to honour, and the callee here is an interface written for code we do
not own; the shipped Entra driver does honour it, but a driver that blocks on a call ignoring the
context would hold the goroutine, and with it the rest of the pass (organisations are synced one
after another) and the ticker loop that started it. Running the call on its own goroutine and
selecting against `ctx.Done()` costs one leaked goroutine per wedged driver and keeps
`DefaultSyncTimeout` true. The same goroutine recovers a panic in a driver, which would otherwise
take the API process down.

## 3. Decisions

### 3.1 Pull over Graph, not SCIM push

Entra offers two standard shapes. **SCIM 2.0 push** is the Entra-native "enterprise app
provisioning" path: we expose a SCIM endpoint and Entra's provisioning service calls it with
create/update/deactivate. **Microsoft Graph pull** is what this implements: we read the directory
and reconcile on our side.

Pull was chosen for the first source for two reasons.

1. **It is a smaller thing to get right.** SCIM means a token-authenticated write endpoint, outside
   our session model, that can create and remove memberships in an organisation, plus SCIM's own
   semantics (PATCH operation syntax, filters, ETags, pagination). The pull path is an outbound read
   with a client credential; the only writes are the ones our own store already audits.
2. **It fits the seam.** A SCIM server is not a `Provisioner` any other source could implement; it
   is a second way in. Adding it later means a package alongside this one that translates SCIM
   requests into the same reconciler operations, not a driver behind `Fetch`.

Nothing here rules SCIM out. If a tenant needs push (near-real-time deactivation is the usual
reason), the reconciler's per-person operations are the reusable part.

### 3.2 Provisioning creates the shell, the wallet still binds the person

This is the open question the issue raised, and the answer is: **a provisioned account becomes an
invitation, not a membership.** The person still completes wallet identity disclosure on accept, and
the disclosed passport/ID-card name and e-mail are still matched against what was invited
(`organization/accept.go`, `admin_reviews.go`). None of that path changes.

The alternative, trusting a directory export to assert who somebody legally is, would quietly
demote the thing this product exists for. A directory says "this mailbox belongs to an employee
record named X"; it does not say "the person holding this wallet is X". So the directory is
authoritative for *membership and attributes*, and the wallet stays authoritative for *identity*.

The visible consequence: a provisioned person shows in the member list with status `invited` until
they accept, exactly like a hand-typed invitation. Role, job title and department are carried on the
invitation and applied when the membership is created.

### 3.3 The sync only touches what it created

`provisioned_members` and `provisioned_departments` record which invitations, memberships and
departments the sync owns. A row with no link is never modified and never deprovisioned, so the
manual invitation flow and the directory sync coexist in one organisation.

That settles the conflict question: **a source account whose e-mail already belongs to a hand-made
membership is not adopted.** It is reported in the run's `skipped` list with reason `conflict`, on
every run, until an admin resolves it. Taking it over would let a directory change somebody's role
in an organisation they were admitted to by hand.

Departments are the one place adoption does happen: a source department whose name already exists as
a department is linked rather than duplicated, because the name is unique per organisation and
creating it would fail. Adopting only records the link; the department is otherwise untouched.

### 3.4 Role mapping

`adminGroupIds` lists the source groups whose members get `RoleAdmin`. Anyone else gets `RoleMember`.
The mapping is resolved by the driver (only it knows how to ask the source about group membership)
and is one call per admin group, not one per person.

Demoting the organisation's **last** admin is refused by the membership store and reported as
`skipped: last_admin`. A directory must not be able to lock everyone out of the wallet.

### 3.5 Safety rules the reconciler enforces

- **An empty directory is refused** (`ErrEmptyDirectory`), not obeyed. An expired secret, a mistyped
  group id and a revoked Graph permission all come back as a successful read of zero accounts, and
  obeying that would deprovision the whole organisation in one pass.
- **A disabled account deprovisions**, like a disappeared one. `accountEnabled: false` is a decision
  somebody made about that person. An *absent* `accountEnabled` is treated as enabled: Graph omits
  it for objects the app registration may list but not fully read, and reading "not told" as
  "disabled" would empty the organisation.
- **Somebody removed here is not re-invited.** If the sync owns a person but their invitation or
  membership is gone (an admin revoked it, or they declined), the run reports
  `skipped: removed_locally` and leaves it. Re-creating it would restart that argument, and mail
  the person, on every pass. Removing them in the source is what stops the report.
- **A paging link that leaves the Graph endpoint is refused**, so a spoofed response cannot walk our
  bearer token to a host of its choosing.
- **Departed accounts let go of their address before anybody is invited.** `provisioned_members`
  allows one link per address per source, so a person deleted in the source and re-created on the
  same mailbox needs the old link gone before the new account can take it. The stale links are
  cleared first, and an address a still-listed account holds is reported as `skipped: conflict`
  rather than invited — the invitation would exist with nothing owning it and the run would abort on
  the link write.
- **A department is only mirrored for somebody the run provisions.** Departments are never deleted
  here, so one created for a disabled or unusable record would sit in the organisation's list, empty,
  for good. A record skipped as `conflict` or `removed_locally` is not knowable until the membership
  lookup and still mirrors its department.

### 3.6 Known limits

- **Group membership is read transitively.** Both the scoping `groupId` and each of `adminGroupIds`
  are read through Graph's `transitiveMembers` collection, not `members`, because `members` returns
  direct members only and nesting is ordinary (an "All staff" group holding one group per
  department). On the scoping group direct-only would at least fail loudly — no users trips
  `ErrEmptyDirectory` — but on an admin group it would resolve nobody and quietly provision every
  admin as a plain member. Same permission either way (`GroupMember.Read.All`).

- **A department rename in the source reads as a new department.** Entra's `department` is a string
  attribute on the user with no stable identifier behind it, so there is nothing to detect a rename
  by. The old department is left alone (it may still hold manually-added members, and the store
  refuses to delete a department that does). A rename *here* is followed correctly: the link, not
  the name, says which department mirrors the source's. Entra administrative units would give stable
  ids, and the `Directory` type can carry them without changing the seam.
- **An e-mail change in the source is not followed.** Invitations and memberships are keyed by
  e-mail, and the wallet disclosure on accept is matched against it, so the address recorded at
  provisioning time is the one that person stays under.
- **Pending invitations keep their token.** A role/department change on a not-yet-accepted person
  rewrites the invitation in place (`organization.Store.UpdateInvitation`) rather than revoking and
  reissuing it, so a link already mailed keeps working.

## 4. Shape

```
provisioning.Scheduler ── every hour, per enabled org ──▶ provisioning.Service.Sync
                                                            │
POST /orgs/{slug}/provisioning/sync ────────────────────────┘
                                                            │
                       provisioner.Provisioner.Fetch ◀──────┤  (Entra: token, users, admin groups)
                                                            │
                       organization.Store ◀─────────────────┘  (departments, invitations, memberships)
```

Every mutation goes through `organization.Store`, so a provisioned change is audited exactly like the
same change made by hand (`membership.invited`, `membership.invite_updated`, `membership.revoked`,
`department.created`, …), with no actor on a scheduled run. The run itself adds
`provisioning.run_completed` / `provisioning.run_failed`, whose metadata is counts only: an audit
event is readable by every org admin, and a skip says something about the tenant's directory rather
than about the organisation's own members.

The three run actions are deliberately **not** in the notifications catalogue
(`internal/notifications`). That catalogue is a data-minimisation gate, not a display list, and a
failed run's metadata is the source's own error text. If an org asks to be paged on a failed sync, it
needs a metadata shape decided for that purpose first.

A provisioned change is audited but does **not** notify. The sync writes memberships through an
`organization.Store` built on the plain `audit.DBRecorder` rather than the notifications one
(`cmd/api`), so its `membership.*` and `department.*` events never reach the notification outbox.
The reason is volume, not secrecy: those three actions are all in the catalogue, so a first run
against a directory of five hundred people would mail every admin five hundred times for one act of
configuration, and every leaver sweep after that would arrive in bursts. This is the third
documented exception to "every store gets the shared recorder", beside the seeder and the
notification store itself, and it is one argument to reverse if an organisation would rather be
paged about provisioned joiners and leavers.

## 5. Configuration

Deployment: `PROVISIONING_ENCRYPTION_KEY` (hex 32 bytes) encrypts each organisation's directory
client secret at rest, exactly like `EMAIL_ENCRYPTION_KEY` does for SMTP passwords. Without it an
organisation cannot store a secret, so provisioning stays unavailable.

Per organisation (`PUT /api/v1/orgs/{slug}/provisioning/settings`, org-admin only):

| Field | Meaning |
|---|---|
| `enabled` | Whether the hourly sync runs. |
| `source` | `entra`. |
| `tenantId` | Entra tenant GUID or domain. |
| `clientId` / `clientSecret` | The app registration the sync authenticates as. The secret is write-only: omit it to keep the stored one, send it empty to clear it. |
| `groupId` | Group whose members are in scope. Empty reads the whole directory. |
| `adminGroupIds` | Groups mapped to the admin role. |

The app registration needs the application permissions `User.Read.All` and `GroupMember.Read.All`,
admin-consented in the tenant.

`POST /api/v1/orgs/{slug}/provisioning/sync` runs one now and answers with the counts and the skip
list, which is how an admin checks a credential they just fixed. It carries the same
`DefaultSyncTimeout` the scheduled pass puts on one organisation, and answers 504 when it runs out.
A failure of the source is a 502 and a failure of ours is a 500: telling an admin their directory is
at fault when a query of ours failed sends them to check a credential that is fine.
