-- +goose Up
-- Lets an openid4vp_transactions row originate from a QERDS message instead of a
-- browser invocation (issue #271): org A's Authorization Request travels as a
-- QERDS body part, org B's Receiver validates it exactly like the browser start
-- endpoint and queues it straight at org_selected, since the receiving address
-- already resolves the organization. source_message_id is that row's idempotency
-- key, mirroring credential_offers.source_message_id: a re-delivered message
-- resolves to the same transaction rather than queuing a second one.
ALTER TABLE openid4vp_transactions
    ADD COLUMN source_message_id UUID REFERENCES qerds_messages (id) ON DELETE CASCADE;

-- Partial: only QERDS-originated rows carry a source_message_id, and only they
-- need the per-organization uniqueness a re-delivery relies on.
CREATE UNIQUE INDEX idx_openid4vp_transactions_source_message
    ON openid4vp_transactions (organization_id, source_message_id)
    WHERE source_message_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_openid4vp_transactions_source_message;
ALTER TABLE openid4vp_transactions
    DROP COLUMN source_message_id;
