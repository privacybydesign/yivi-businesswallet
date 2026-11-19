-- +goose Up
-- Review items tell the org's pages through the wallet's event stream now.
DROP TRIGGER identity_proofing_review_items_notify_reviews ON identity_proofing_review_items;
DROP FUNCTION notify_identity_proofing_review_items();

CREATE TRIGGER identity_proofing_review_items_event
AFTER INSERT OR UPDATE OR DELETE ON identity_proofing_review_items
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('reviews', 'organization_id');

-- +goose Down
DROP TRIGGER identity_proofing_review_items_event ON identity_proofing_review_items;

-- +goose StatementBegin
CREATE FUNCTION notify_identity_proofing_review_items() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('identity_proofing_reviews', COALESCE(NEW.organization_id, OLD.organization_id)::text);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER identity_proofing_review_items_notify_reviews
AFTER INSERT OR UPDATE OR DELETE ON identity_proofing_review_items
FOR EACH ROW
EXECUTE FUNCTION notify_identity_proofing_review_items();
