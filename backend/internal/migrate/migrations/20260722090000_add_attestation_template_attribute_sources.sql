-- +goose Up
-- attribute_sources binds a schema attribute key to a subject-field token (e.g.
-- "member.email", "org.kvkNumber"), so the issue wizard can pre-fill that attribute
-- from the selected recipient's known data instead of requiring it to be retyped.
-- It sits alongside default_attributes (static values); a binding takes precedence
-- as the pre-fill, and the value stays editable in the wizard.
ALTER TABLE attestation_templates
    ADD COLUMN attribute_sources JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE attestation_templates
    DROP COLUMN attribute_sources;
