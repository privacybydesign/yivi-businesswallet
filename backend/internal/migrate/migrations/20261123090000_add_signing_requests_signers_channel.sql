-- +goose Up
-- A finished document may now be mailed to everyone who signed it
-- (recipient_channel 'signers'), which names no recipient address.
ALTER TABLE signing_requests DROP CONSTRAINT signing_requests_recipient_channel_check;
ALTER TABLE signing_requests
    ADD CONSTRAINT signing_requests_recipient_channel_check
        CHECK (recipient_channel IN ('none', 'qerds', 'email', 'signers'));

-- +goose Down
-- A request that mailed its signers has no other recipient to fall back to.
UPDATE signing_requests
    SET recipient_channel = 'none', delivery_status = 'not_requested'
    WHERE recipient_channel = 'signers';
ALTER TABLE signing_requests DROP CONSTRAINT signing_requests_recipient_channel_check;
ALTER TABLE signing_requests
    ADD CONSTRAINT signing_requests_recipient_channel_check
        CHECK (recipient_channel IN ('none', 'qerds', 'email'));
