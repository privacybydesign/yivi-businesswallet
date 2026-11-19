-- +goose Up
-- A signing credential of the wallet's own signing service (internal/yivicsc): a
-- person's signing key and the certificate over it. A person is named by
-- subject_key, a keyed hash of the name and date of birth they disclosed with
-- Yivi and of where that name came from, so the row can be found again by the
-- same disclosure without holding the date of birth. The name itself is in the
-- certificate.
--
-- `source` is the family the name was disclosed from: `mrtd` (passport, identity
-- card or residence permit) or `brp` (the registered personal data). A signature
-- is authorised by a disclosure from the same family.
--
-- A person has one row: an expired credential is replaced in place under a new
-- id. The private key is sealed under CSC_ENCRYPTION_KEY (AES-256-GCM).
CREATE TABLE yivi_csc_credentials
(
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_key            BYTEA       NOT NULL UNIQUE,
    source                 TEXT        NOT NULL CHECK (source IN ('mrtd', 'brp')),
    certificate_der        BYTEA       NOT NULL,
    private_key_ciphertext BYTEA       NOT NULL,
    not_after              TIMESTAMPTZ NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE yivi_csc_credentials;
