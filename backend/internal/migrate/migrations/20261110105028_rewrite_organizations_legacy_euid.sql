-- +goose Up
-- NL.KVK.<kvk> was our own placeholder, not an EUID. WE BUILD writes NLNHR.<kvk>.
UPDATE organizations
SET euid = 'NLNHR.' || substring(euid FROM 8)
WHERE euid ~ '^NL\.KVK\.[0-9]{8}$';

-- +goose Down
-- No-op: the placeholder form is not restored.
SELECT 1;
