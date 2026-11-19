-- +goose Up
-- An org's "delete my data" requests are decided by a reviewer ('review') or,
-- for the sessions proven to be the person's, at once ('automatic'). 'keep',
-- which refused every one, is gone: what the law makes an org keep is its
-- legal retention, which declines an erasure only while it holds the data.
-- An org that had 'keep' gets 'review', so nothing it kept is erased without
-- a reviewer.
UPDATE identity_proofing_settings
SET erasure_handling = 'review', updated_at = now()
WHERE erasure_handling = 'keep';

ALTER TABLE identity_proofing_settings
    DROP CONSTRAINT identity_proofing_settings_erasure_handling_check,
    ADD CONSTRAINT identity_proofing_settings_erasure_handling_check
        CHECK (erasure_handling IN ('review', 'automatic'));

-- +goose Down
-- 'keep' is allowed again. Which orgs had it is not kept: they stay 'review'.
ALTER TABLE identity_proofing_settings
    DROP CONSTRAINT identity_proofing_settings_erasure_handling_check,
    ADD CONSTRAINT identity_proofing_settings_erasure_handling_check
        CHECK (erasure_handling IN ('review', 'automatic', 'keep'));
