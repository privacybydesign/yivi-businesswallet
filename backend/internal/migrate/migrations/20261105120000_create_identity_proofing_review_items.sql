-- +goose Up
-- One row per part of a session a reviewer decides on its own. A session's
-- own review (needs_review) stays on the request.
CREATE TABLE identity_proofing_review_items (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id      UUID        NOT NULL REFERENCES identity_proofing_requests (id) ON DELETE CASCADE,
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind            TEXT        NOT NULL CHECK (kind IN ('identity', 'address', 'employment', 'data_request')),
    state           TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    due_at          TIMESTAMPTZ NOT NULL,
    decided_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    decided_at      TIMESTAMPTZ,
    reason          TEXT,
    error_code      TEXT,
    CHECK (CASE WHEN decided_at IS NULL THEN state = 'pending' ELSE state IN ('confirmed', 'rejected', 'lapsed') END)
);

CREATE UNIQUE INDEX identity_proofing_review_items_one_pending
    ON identity_proofing_review_items (request_id, kind) WHERE state = 'pending';
CREATE INDEX identity_proofing_review_items_request ON identity_proofing_review_items (request_id, kind);
CREATE INDEX identity_proofing_review_items_org ON identity_proofing_review_items (organization_id, kind, state);
CREATE INDEX identity_proofing_review_items_due ON identity_proofing_review_items (state, due_at);

-- What a reviewer sees changed, as notify_identity_proofing_reviews says for
-- the requests. A delete (a purge) has only OLD.
-- +goose StatementBegin
CREATE FUNCTION notify_identity_proofing_review_items() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('identity_proofing_reviews', COALESCE(NEW.organization_id, OLD.organization_id)::text);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER identity_proofing_review_items_notify_reviews
AFTER INSERT OR UPDATE OR DELETE ON identity_proofing_review_items
FOR EACH ROW
EXECUTE FUNCTION notify_identity_proofing_review_items();

-- +goose Down
DROP TABLE identity_proofing_review_items;
DROP FUNCTION notify_identity_proofing_review_items();
