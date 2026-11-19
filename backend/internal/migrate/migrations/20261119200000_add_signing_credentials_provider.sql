-- +goose Up
-- A linked signing credential belongs to the provider it was linked at: its id
-- names a key there and nothing anywhere else. provider_base_url records that
-- provider, so a signer whose organisation changes provider is asked to link
-- again instead of having the old id sent to the new provider, which refuses it.
ALTER TABLE signing_credentials
    ADD COLUMN provider_base_url TEXT NOT NULL DEFAULT '';

-- Credentials linked before this column existed were linked at the remote
-- provider the organisation has saved, where it still signs with one. Any other
-- (the organisation moved to the built-in service since) keeps '', matches no
-- provider, and is linked again on the signer's next visit.
UPDATE signing_credentials c
SET provider_base_url = s.base_url
FROM org_csc_settings s
WHERE s.organization_id = c.organization_id
  AND s.enabled
  AND s.provider_kind <> 'yivi'
  AND s.base_url <> '';

-- +goose Down
ALTER TABLE signing_credentials DROP COLUMN provider_base_url;
