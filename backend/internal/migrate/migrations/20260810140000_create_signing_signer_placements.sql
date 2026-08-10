-- +goose Up
-- Where each signer's visible marks land in the document. A signer may have at
-- most one `signature` placement (a PAdES signature carries exactly one visible
-- appearance, so a second one could not be rendered) and at most one `paraph` per
-- page. "Paraph on every page" is expanded by the requester into one row per page,
-- so this table stays flat: every row is one rectangle on one page.
--
-- The rectangle is in PDF user-space points with the origin at the page's
-- bottom-left, the same space pdfsign's appearance rectangle is in — the requester
-- converts from viewer coordinates once, at placement time, so nothing downstream
-- has to know the zoom, the rotation or the crop box the document was placed at.
CREATE TABLE signing_signer_placements
(
    id        UUID             NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    signer_id UUID             NOT NULL REFERENCES signing_request_signers (id) ON DELETE CASCADE,
    kind      TEXT             NOT NULL CHECK (kind IN ('signature', 'paraph')),
    page      INT              NOT NULL CHECK (page >= 1),
    x         DOUBLE PRECISION NOT NULL CHECK (x >= 0),
    y         DOUBLE PRECISION NOT NULL CHECK (y >= 0),
    width     DOUBLE PRECISION NOT NULL CHECK (width > 0),
    height    DOUBLE PRECISION NOT NULL CHECK (height > 0)
);

-- Loads one request's placements alongside its signers.
CREATE INDEX idx_signing_signer_placements_signer ON signing_signer_placements (signer_id);
CREATE UNIQUE INDEX idx_signing_signer_placements_signature
    ON signing_signer_placements (signer_id) WHERE kind = 'signature';
CREATE UNIQUE INDEX idx_signing_signer_placements_paraph
    ON signing_signer_placements (signer_id, page) WHERE kind = 'paraph';

-- +goose Down
DROP TABLE signing_signer_placements;
