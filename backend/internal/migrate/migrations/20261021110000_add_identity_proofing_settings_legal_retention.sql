-- +goose Up
-- A legal retention: how long the org must keep the data of every approved
-- identity check by law, counted from the end of the check, and why, in each
-- language the person's pages speak. It is the least any flow keeps that data,
-- and no "delete my data" request erases a session before it has passed. NULL
-- is no legal period; the months and both reasons are set together. The bound
-- is 50 years; the app offers years and months.
--
-- engine_retention_stale marks an org whose engine sessions may not hold the
-- retention its settings give them yet: a legal retention was saved, or a
-- session was created against settings that changed before it was attached.
-- The engine fixes a session's retention when it creates it, so the wallet
-- sets it again (proofing.Service.syncEngineRetention) and clears the mark;
-- the pruner's next run retries one that failed.
ALTER TABLE identity_proofing_settings
    ADD COLUMN legal_retention_months    INTEGER CHECK (legal_retention_months BETWEEN 1 AND 600),
    ADD COLUMN legal_retention_reason_en TEXT CHECK (btrim(legal_retention_reason_en) <> ''),
    ADD COLUMN legal_retention_reason_nl TEXT CHECK (btrim(legal_retention_reason_nl) <> ''),
    ADD COLUMN engine_retention_stale    BOOLEAN NOT NULL DEFAULT false,
    ADD CONSTRAINT identity_proofing_settings_legal_retention_check CHECK (
        (legal_retention_months IS NULL) = (legal_retention_reason_en IS NULL)
        AND (legal_retention_months IS NULL) = (legal_retention_reason_nl IS NULL));

-- +goose Down
-- Without the legal period an unpurged external identity check is purged its
-- retention after it settled, as setPurgeAt counted before it: the flow's
-- override, else the org's data retention. A check the legal period held past
-- that is then due, and the pruner erases it, its engine session with it.
UPDATE identity_proofing_requests r
SET purge_at = COALESCE(r.cancelled_at, r.ips_session_ended_at,
                        CASE
                            WHEN r.status IN ('approved', 'rejected')
                                OR (r.status = 'needs_review' AND r.flow_kind = 'identity') THEN r.completed_at END,
                        CASE WHEN r.status IN ('pending', 'in_progress') THEN r.ips_session_expires_at END,
                        CASE WHEN r.status = 'pending' AND r.ips_session_id IS NULL THEN r.link_expires_at END)
                   + COALESCE(make_interval(secs => r.retention_override_seconds),
                              make_interval(days => st.data_retention_days))
FROM identity_proofing_settings st
WHERE st.organization_id = r.organization_id
  AND st.legal_retention_months IS NOT NULL
  AND r.subject_kind = 'external'
  AND r.flow_kind = 'identity'
  AND r.purged_at IS NULL;

ALTER TABLE identity_proofing_settings
    DROP CONSTRAINT identity_proofing_settings_legal_retention_check,
    DROP COLUMN engine_retention_stale,
    DROP COLUMN legal_retention_reason_nl,
    DROP COLUMN legal_retention_reason_en,
    DROP COLUMN legal_retention_months;
