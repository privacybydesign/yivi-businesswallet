-- +goose Up
-- A hosted request on a flow that matches the face against the customer's own
-- photo (no chip read) holds that photo, sealed, only until its subject starts
-- the session: it then moves to the engine's session, which drops it once the
-- face check can no longer run.
ALTER TABLE identity_proofing_requests
    ADD COLUMN reference_photo_ciphertext BYTEA,
    ADD COLUMN reference_photo_mime TEXT,
    ADD CONSTRAINT identity_proofing_requests_reference_photo_check
        CHECK ((reference_photo_ciphertext IS NULL) = (reference_photo_mime IS NULL));

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_reference_photo_check,
    DROP COLUMN reference_photo_mime,
    DROP COLUMN reference_photo_ciphertext;
