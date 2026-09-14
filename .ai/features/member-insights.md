# Feature: Member insights (dashboard overview)

**Status:** Implemented. An org admin's dashboard opens with how many members
have not identified and how many have a VOG that needs attention, with the
per-status breakdown behind each number.
**Slice:** `internal/organization/member_insights.go` (store query, pure tally,
`GET /orgs/{slug}/member-insights`), `frontend/src/routes/member-insights.tsx`,
`frontend/src/lib/member-insights.ts`.
**Builds on:** `.ai/features/member-reidentification.md` §2 (identity status)
and `.ai/features/member-screening-vog.md` §3 (screening status) - it only
counts what those derive.

## Counted server-side, derived on read

`MemberStatusSnapshots` reads every active membership's identity and screening
columns for the org, unpaged; `TallyMemberInsights` runs `DeriveIdentityStatus`
and `DeriveScreeningStatus` over them under the org's current policy and
counts. Nothing is stored; a policy change or the passage of time moves the
numbers the moment they are read, exactly like the member list's badges - and
because it is the same derivation, the overview and the list can never
disagree.

Server-side rather than tallying the member list in the browser because that
list is paged (`MaxMemberListLimit` = 100): a client-side count would silently
undercount any org past one page, the "count that disagrees with the page"
failure `.ai/features/member-reidentification.md` §11 warns about. Every known
status is present in the response with 0 when empty, so the client never has
to know the status list to draw a complete bar.

Active memberships only. A pending invitation has no identity or screening to
speak of, so it would only inflate "never" and "none".

## The two headline numbers are frontend definitions

`frontend/src/lib/member-insights.ts` decides what "not identified" and "VOG
needs attention" mean, tested as pure functions:

| headline | statuses |
|---|---|
| not identified | `never`, `requested`, `overdue` (`due_soon` is still identified) |
| VOG needs attention | `none`, `requested`, `rejected`, `expired`, `recheck_required` (`expiring` is still valid today; `not_required` is nobody's problem) |

The screening bar and headline are suppressed (shown as "—" with a hint) when
every member is `not_required`, i.e. the policy requires a VOG of nobody.

## No charting library

The stacked proportional bars are divs (the way `ui/stepper.tsx` draws its
meter), coloured from the same tone functions the member list's `Tag`s use
(`identityStatusTone`, `screeningStatusTone`) so bar, legend and list agree. A
status the backend adds before the frontend knows it is appended to the bar
rather than dropped (`statusSegments`). The Vitest setup is `node`-only and
skips `.test.tsx`, so a chart component would have been untestable; the logic
that matters is in `lib/` and tested.

## Not built

- **Filtering the member list by identity or VOG status** from a bar segment.
  The link goes to the unfiltered list; a status filter has to be expressed
  in SQL to keep paging totals honest (see the re-identification doc's §11).
- **Trends over time.** The overview is a snapshot; nothing is recorded.
