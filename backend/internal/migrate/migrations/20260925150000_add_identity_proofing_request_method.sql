-- +goose Up
-- method is how the subject took part, as IPS reports it: idem_app (the Idem
-- app read the document's chip), yivi_app (a disclosure from the Yivi app) or
-- browser. NULL while no device has claimed the session, which stays so for a
-- session nobody opened.
ALTER TABLE identity_proofing_requests
    ADD COLUMN method TEXT;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN method;
