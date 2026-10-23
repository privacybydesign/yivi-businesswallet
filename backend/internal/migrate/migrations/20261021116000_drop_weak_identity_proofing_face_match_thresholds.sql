-- +goose Up
-- The Regula verifier used to match at the deployment's threshold
-- (REGULA_FACE_MATCH_THRESHOLD, 0.75 by default), and a flow's own face.match
-- threshold could only make it stricter. Now a flow's threshold replaces the
-- deployment's, lower ones too, so a version stored with one under 0.75 (the
-- old editor took any number from 0 to 1) would accept weaker matches the
-- moment this deploys, without anyone editing the flow. Such a threshold is
-- dropped from every version, active or not (a session runs the version it
-- was created on, and an older one can be activated again), so the flow
-- matches at the deployment's threshold as it did. One of 0.75 or more held
-- before and stays; a lower one is the admin's to set in the flow editor,
-- with its warning. On a deployment set under 0.75, a dropped threshold
-- between the two made the match stricter there; that flow now matches at
-- the deployment's.
UPDATE identity_proofing_flow_versions
SET definition = definition #- '{CheckThresholds,face.match}'
WHERE (definition -> 'CheckThresholds' ->> 'face.match')::double precision < 0.75;

-- +goose Down
-- Intentionally empty. Under the code before the Up, a threshold under the
-- deployment's had no effect, so each flow runs as it did without the ones
-- dropped; giving them back would lower the bar again under the code that
-- came with the Up.
