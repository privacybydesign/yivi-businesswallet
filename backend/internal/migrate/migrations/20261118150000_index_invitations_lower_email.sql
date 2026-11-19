-- +goose Up
-- The org switcher and login look invitations up by lower(email), which the
-- plain email index cannot serve.
CREATE INDEX idx_invitations_lower_email ON invitations (lower(email));

-- +goose Down
DROP INDEX idx_invitations_lower_email;
