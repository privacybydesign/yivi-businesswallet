-- +goose Up
-- A legal retention: how long the org must keep the data of every identity
-- check by law, counted from the end of the check, and why, in each language
-- the person's pages speak. It is the least any flow keeps that data, and no
-- "delete my data" request erases a session before it has passed. NULL is no
-- legal period; the months and both reasons are set together. The bound is
-- 50 years; the app offers years and months.
ALTER TABLE identity_proofing_settings
    ADD COLUMN legal_retention_months    INTEGER CHECK (legal_retention_months BETWEEN 1 AND 600),
    ADD COLUMN legal_retention_reason_en TEXT CHECK (btrim(legal_retention_reason_en) <> ''),
    ADD COLUMN legal_retention_reason_nl TEXT CHECK (btrim(legal_retention_reason_nl) <> ''),
    ADD CONSTRAINT identity_proofing_settings_legal_retention_check CHECK (
        (legal_retention_months IS NULL) = (legal_retention_reason_en IS NULL)
        AND (legal_retention_months IS NULL) = (legal_retention_reason_nl IS NULL));

-- +goose Down
ALTER TABLE identity_proofing_settings
    DROP CONSTRAINT identity_proofing_settings_legal_retention_check,
    DROP COLUMN legal_retention_reason_nl,
    DROP COLUMN legal_retention_reason_en,
    DROP COLUMN legal_retention_months;
