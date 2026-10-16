-- +goose Up
-- How a customer's "delete my data" requests are decided once the person is
-- proven: 'review' by a reviewer; 'automatic' erases the sessions proven to be
-- the person's at once, and leaves a reviewer only those matched on an e-mail
-- address or typed name; 'keep' refuses every one, for a customer bound by law
-- to keep the data (LEGAL_RETENTION).
ALTER TABLE identity_proofing_customers
    ADD COLUMN erasure_handling TEXT NOT NULL DEFAULT 'review'
        CHECK (erasure_handling IN ('review', 'automatic', 'keep'));

-- How many of an approved erasure's matched sessions were kept, so its person
-- is told not everything was deleted. Set with the decision; the request's own
-- matches go with its purge, which follows at once.
ALTER TABLE identity_proofing_requests
    ADD COLUMN data_matches_kept INTEGER
        CHECK (data_matches_kept IS NULL OR (data_matches_kept >= 0 AND flow_kind = 'data_erasure'));

-- When a delivery was first tried: last_attempt_at moves with every retry, so
-- an endpoint's failing streak counts from this. A delivery tried before the
-- column existed counts from its latest attempt, as it did.
ALTER TABLE identity_proofing_webhook_deliveries
    ADD COLUMN first_attempt_at TIMESTAMPTZ;
UPDATE identity_proofing_webhook_deliveries SET first_attempt_at = last_attempt_at
    WHERE last_attempt_at IS NOT NULL;

-- +goose Down
ALTER TABLE identity_proofing_webhook_deliveries
    DROP COLUMN first_attempt_at;

ALTER TABLE identity_proofing_requests
    DROP COLUMN data_matches_kept;

ALTER TABLE identity_proofing_customers
    DROP COLUMN erasure_handling;
