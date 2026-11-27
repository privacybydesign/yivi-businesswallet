-- +goose Up
-- Whether we as wallet provider are on the trusted list (Art 12(5)). One row:
-- missing_since is when a check first read us as off the list, delisted_at when
-- the grace ran out and every org's wallet was suspended (Art 6(2)(f)).
CREATE TABLE provider_listing_state
(
    id            BOOLEAN     PRIMARY KEY DEFAULT true CHECK (id),
    missing_since TIMESTAMPTZ,
    delisted_at   TIMESTAMPTZ,
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The wallets suspended because we were off the list, so being back on it
-- resumes these and not one an admin suspended.
CREATE TABLE provider_suspended_wallets
(
    organization_id UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    suspended_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The notice each org still has to get, retried every check until it is sent:
-- Annex 6(2) gives 24 hours from the revocation. warned_at is when we were told
-- it was still unsent.
CREATE TABLE provider_listing_notices
(
    organization_id UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    kind            TEXT        NOT NULL CHECK (kind IN ('delisted', 'relisted')),
    queued_at       TIMESTAMPTZ NOT NULL,
    warned_at       TIMESTAMPTZ
);

-- The alerts to us not yet sent, queued with what caused them and retried every
-- check.
CREATE TABLE provider_listing_alerts
(
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    text      TEXT        NOT NULL,
    queued_at TIMESTAMPTZ NOT NULL
);

-- The stub trusted list a deployment without WE BUILD access checks against.
-- It lists us until a test takes us off.
CREATE TABLE stub_trusted_list
(
    id     BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    listed BOOLEAN NOT NULL
);

INSERT INTO stub_trusted_list (listed) VALUES (true);

-- +goose Down
DROP TABLE stub_trusted_list;
DROP TABLE provider_listing_alerts;
DROP TABLE provider_listing_notices;
DROP TABLE provider_suspended_wallets;
DROP TABLE provider_listing_state;
