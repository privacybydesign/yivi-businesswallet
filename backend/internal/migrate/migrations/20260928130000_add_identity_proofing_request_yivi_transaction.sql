-- +goose Up
-- yivi_transaction_id is the OpenID4VP verifier's transaction of a Yivi
-- request's disclosure (the passport or id-card and its photo), which the
-- wallet redeems to hand the photo to IPS for the face check. Server-side
-- only; NULL for an Idem request and before the disclosure starts.
ALTER TABLE identity_proofing_requests
    ADD COLUMN yivi_transaction_id TEXT;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN yivi_transaction_id;
