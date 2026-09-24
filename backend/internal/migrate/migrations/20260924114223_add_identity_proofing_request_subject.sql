-- +goose Up
-- A proofing request is now sent to a member of the org (employee or external),
-- chosen from the member list, and its IPS session is created when the mail is
-- sent: the mailed link lives exactly as long as that session (IPS caps it at
-- 15 minutes), so link_expires_at is the session's expiry.
--
-- subject_user_id is the member the request was sent to. subject_name and
-- subject_email stay as the snapshot the mail went to, so a request still reads
-- after the member leaves (ON DELETE SET NULL). Requests from before this
-- migration were free-form and have no subject user.
--
-- flow_version is the IPS flow version the session pinned; later versions of the
-- flow do not change what a sent request runs.
ALTER TABLE identity_proofing_requests
    ADD COLUMN subject_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN flow_version    INTEGER;

CREATE INDEX identity_proofing_requests_org_subject_idx
    ON identity_proofing_requests (organization_id, subject_user_id, created_at DESC);

-- +goose Down
DROP INDEX identity_proofing_requests_org_subject_idx;
ALTER TABLE identity_proofing_requests
    DROP COLUMN flow_version,
    DROP COLUMN subject_user_id;
