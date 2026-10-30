-- +goose Up
-- A purge now clears an address review still pending, as it erased the address
-- it was about. Clear the ones purged before that.
UPDATE identity_proofing_requests
SET address_review = NULL
WHERE purged_at IS NOT NULL AND address_review = 'pending';

-- +goose Down
-- Nothing to restore: no reviewer could decide a purged address.
