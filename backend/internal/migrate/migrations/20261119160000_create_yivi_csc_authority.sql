-- +goose Up
-- The certificate authority of the wallet's own signing service (internal/yivicsc):
-- the key that signs every signing certificate the service issues, and its
-- self-signed certificate. A deployment has one, made on first use, so the table
-- holds one row: `singleton` is the primary key and can only be true.
--
-- The private key is sealed under CSC_ENCRYPTION_KEY (AES-256-GCM); the row never
-- holds it in the clear.
CREATE TABLE yivi_csc_authority
(
    singleton              BOOLEAN     PRIMARY KEY DEFAULT true CHECK (singleton),
    certificate_der        BYTEA       NOT NULL,
    private_key_ciphertext BYTEA       NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE yivi_csc_authority;
