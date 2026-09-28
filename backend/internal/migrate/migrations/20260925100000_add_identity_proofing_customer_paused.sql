-- +goose Up
-- paused_at is when an admin paused proofing for a customer: while it is set, no
-- new request can be sent for the customer. Requests already sent run out as
-- usual. NULL is an active customer.
ALTER TABLE identity_proofing_customers
    ADD COLUMN paused_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_customers
    DROP COLUMN paused_at;
