-- +goose Up
-- identity_proofing_requests is one ask for a person (the subject) to prove
-- their identity on one of the org's flows. It goes to a member of the org
-- (subject_user_id), or to an external subject of one of the org's customers
-- (customer_id): a person known only by an e-mail address and an optional
-- name, never a wallet user. A member sends it from the app (requested_by), a
-- customer's backend creates it through the API (api_key_id), or a hosted
-- link starts it.
--
-- The session itself runs at the proofing provider (the engine's
-- identity_proofing_sessions); the ips_session_* columns track it. A mailed
-- request carries the session from the send, so link_expires_at is that
-- session's expiry. A hosted request carries a link instead, and its session
-- is created when the subject starts.
--
-- Only the outcome is kept: status, the achieved assurance levels and the
-- error code, plus, for a customer's subject, the name read off the document
-- until the customer's retention lapses. Never the document fields, BSN or
-- images the check saw.
--
-- status: pending (no live session), in_progress (a live session), then
-- approved / rejected / needs_review. needs_review is not final, so it keeps
-- being reconciled. "expired" is derived from the deadlines, not stored.
CREATE TABLE identity_proofing_requests
(
    id                             UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id                UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- A test request runs on scripted outcomes (a test API key).
    mode                           TEXT        NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test')),

    -- Who asked: the member who sent it, or the customer key that created it
    -- (requested_by then stays NULL).
    requested_by                   UUID        REFERENCES users (id) ON DELETE SET NULL,
    api_key_id                     UUID        REFERENCES identity_proofing_api_keys (id) ON DELETE SET NULL,
    customer_id                    UUID,

    -- The subject. subject_name and subject_email are the snapshot the request
    -- went to, so it still reads after the member leaves; subject_name is ''
    -- when no name was given.
    subject_user_id                UUID        REFERENCES users (id) ON DELETE SET NULL,
    subject_name                   TEXT        NOT NULL,
    subject_email                  TEXT        NOT NULL,
    -- A request for one known person: the identity approved must be
    -- subject_name, born on the sealed birth date, or it is rejected. The birth
    -- date is held only until the request is decided (or purged);
    -- expects_subject stays, so the outcome still reads as a match.
    expects_subject                BOOLEAN     NOT NULL DEFAULT false,
    expected_birth_date_ciphertext BYTEA,
    -- A hosted request on a flow that matches the face against the customer's
    -- own photo (no chip read) holds that photo, sealed, only until its
    -- subject starts: it then moves to the engine's session.
    reference_photo_ciphertext     BYTEA,
    reference_photo_mime           TEXT,
    -- The language a hosted request opens in.
    language                       TEXT,

    -- The flow as it was when the request was made: the version the session
    -- pinned, the eIDAS level it demanded (low, substantial, high; NULL
    -- demands none, and an approval below it is rejected) and whether it asks
    -- for DUO diploma extracts. A later change to the flow does not change
    -- what a sent request runs.
    flow_id                        TEXT        NOT NULL,
    flow_name                      TEXT        NOT NULL,
    -- What the session is for: an identity check, or the subject asking the
    -- customer for the data held of them (data_access) or its erasure
    -- (data_erasure). A data request goes to review once the subject is
    -- proven, with their matched sessions (identity_proofing_request_matches,
    -- below); data_export_until is until when an approved data_access
    -- request's data downloads.
    flow_kind                      TEXT        NOT NULL DEFAULT 'identity'
        CHECK (flow_kind IN ('identity', 'data_access', 'data_erasure')),
    data_export_until              TIMESTAMPTZ,
    flow_version                   INTEGER,
    required_assurance_level       TEXT,
    diplomas                       TEXT        NOT NULL DEFAULT 'off' CHECK (diplomas IN ('off', 'required')),

    -- A hosted request is opened from a link: only the SHA-256 of its token is
    -- kept, and redirect_url is where the page hands its subject back.
    link_token_hash                BYTEA,
    link_expires_at                TIMESTAMPTZ NOT NULL,
    redirect_url                   TEXT,

    -- The provider session: its id, its bearer token (sealed), its expiry, and
    -- when the wallet saw it end without an outcome (NULL while it may still
    -- run or decide); a link that is still valid starts a new one. The deadline
    -- job leases the requests it re-checks (ips_reconcile_leased_until), so API
    -- replicas never check the same session at once.
    ips_session_id                 TEXT,
    ips_session_token_ciphertext   BYTEA,
    ips_session_expires_at         TIMESTAMPTZ,
    ips_session_ended_at           TIMESTAMPTZ,
    ips_reconcile_leased_until     TIMESTAMPTZ,
    -- The OpenID4VP verifier's transaction of a Yivi request's disclosure (the
    -- passport or id-card and its photo), which the wallet redeems for the face
    -- check. NULL for an Idem request and before the disclosure starts.
    yivi_transaction_id            TEXT,

    -- The outcome. method is how the subject took part (idem_app, yivi_app or
    -- browser); NULL while no device claimed the session. The proofed name is
    -- the name read off a customer's subject's document (a member already has
    -- one), sealed, and kept only until proofed_name_purge_after.
    status                         TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'approved', 'rejected', 'needs_review')),
    method                         TEXT,
    assurance_level                TEXT,
    eidas_level                    TEXT,
    error_code                     TEXT,
    proofed_name_ciphertext        BYTEA,
    proofed_name_purge_after       TIMESTAMPTZ,

    created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at                   TIMESTAMPTZ,
    -- A customer can cancel a session that has no outcome yet, and erase one:
    -- the row stays, marked, for the audit trail and the API.
    cancelled_at                   TIMESTAMPTZ,
    purged_at                      TIMESTAMPTZ,

    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT identity_proofing_requests_check
        CHECK ((ips_session_id IS NULL) = (ips_session_token_ciphertext IS NULL)),
    CONSTRAINT identity_proofing_requests_check1
        CHECK (customer_id IS NULL OR subject_user_id IS NULL),
    CONSTRAINT identity_proofing_requests_check2
        CHECK ((proofed_name_ciphertext IS NULL) = (proofed_name_purge_after IS NULL)),
    CONSTRAINT identity_proofing_requests_expected_birth_date_check
        CHECK (expected_birth_date_ciphertext IS NULL OR expects_subject),
    CONSTRAINT identity_proofing_requests_reference_photo_check
        CHECK ((reference_photo_ciphertext IS NULL) = (reference_photo_mime IS NULL)),
    CONSTRAINT identity_proofing_requests_data_export_check
        CHECK (data_export_until IS NULL OR flow_kind = 'data_access')
);

CREATE INDEX identity_proofing_requests_org_created_idx
    ON identity_proofing_requests (organization_id, created_at DESC);
CREATE INDEX identity_proofing_requests_org_requester_idx
    ON identity_proofing_requests (organization_id, requested_by, created_at DESC);
CREATE INDEX identity_proofing_requests_org_subject_idx
    ON identity_proofing_requests (organization_id, subject_user_id, created_at DESC);
CREATE INDEX identity_proofing_requests_org_customer_idx
    ON identity_proofing_requests (organization_id, customer_id, created_at DESC);
CREATE INDEX identity_proofing_requests_ips_session_idx
    ON identity_proofing_requests (ips_session_id);
CREATE UNIQUE INDEX identity_proofing_requests_link_token_idx
    ON identity_proofing_requests (link_token_hash) WHERE link_token_hash IS NOT NULL;
-- The pruner finds the proofed names whose retention lapsed.
CREATE INDEX identity_proofing_requests_proofed_name_purge_idx
    ON identity_proofing_requests (proofed_name_purge_after) WHERE proofed_name_purge_after IS NOT NULL;
-- The deadline job finds the live sessions due a re-check...
CREATE INDEX identity_proofing_requests_live_deadline_idx
    ON identity_proofing_requests (ips_session_expires_at)
    WHERE ips_session_id IS NOT NULL AND ips_session_ended_at IS NULL
        AND status IN ('pending', 'in_progress');
-- ...and the hosted links that lapsed unstarted, across every org.
CREATE INDEX identity_proofing_requests_open_link_idx
    ON identity_proofing_requests (link_expires_at)
    WHERE link_token_hash IS NOT NULL AND ips_session_id IS NULL
        AND ips_session_ended_at IS NULL AND status = 'pending';

-- A data request's matches: the customer's sessions whose proofed identity is
-- the subject's, strong when proven with the same document, probable on name
-- and date of birth alone. approved is the reviewer's choice per match, NULL
-- until the decision. A child of identity_proofing_requests on both sides.
CREATE TABLE identity_proofing_request_matches
(
    request_id         UUID NOT NULL REFERENCES identity_proofing_requests (id) ON DELETE CASCADE,
    matched_request_id UUID NOT NULL REFERENCES identity_proofing_requests (id) ON DELETE CASCADE,
    level              TEXT NOT NULL CHECK (level IN ('strong', 'probable')),
    approved           BOOLEAN,
    PRIMARY KEY (request_id, matched_request_id),
    CHECK (request_id <> matched_request_id)
);

CREATE INDEX identity_proofing_request_matches_matched_idx
    ON identity_proofing_request_matches (matched_request_id);

-- +goose Down
DROP TABLE identity_proofing_request_matches;
DROP TABLE identity_proofing_requests;
