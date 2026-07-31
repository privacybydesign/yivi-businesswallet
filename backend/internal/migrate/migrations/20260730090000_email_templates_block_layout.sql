-- +goose Up
-- The tenant-edited mail template becomes an ordered block layout (issue #132's
-- WYSIWYG designer): instead of one fixed shape (headline, paragraphs, call to
-- action, note, footer) a template is a JSONB list of typed blocks — logo,
-- heading, paragraph, button, divider, footer — that an org composes per cause.
-- The blocks are content plus {{variable}} references, never HTML: every type
-- renders through internal/email/shell.go's mail-client-safe markup, and the
-- layout is validated (email.ValidateTemplate) before a row is written.
--
-- Existing overrides are converted in place, in the order the old shell rendered
-- them: wordmark, headline, paragraphs, button, note, footer. The old validation
-- required a headline and set cta_label and cta_url together, so the converted
-- layouts satisfy the new validation too.
ALTER TABLE org_email_templates ADD COLUMN blocks JSONB;

UPDATE org_email_templates SET blocks =
    jsonb_build_array(jsonb_build_object('type', 'logo'))
    || jsonb_build_array(jsonb_build_object('type', 'heading', 'text', headline))
    || COALESCE(
        (SELECT jsonb_agg(jsonb_build_object('type', 'paragraph', 'text', u.paragraph) ORDER BY u.ord)
         FROM unnest(paragraphs) WITH ORDINALITY AS u(paragraph, ord)),
        '[]'::jsonb)
    || CASE WHEN cta_url <> ''
        THEN jsonb_build_array(jsonb_strip_nulls(jsonb_build_object(
            'type', 'button', 'label', cta_label, 'url', cta_url,
            'linkFallback', NULLIF(link_fallback, ''))))
        ELSE '[]'::jsonb END
    || CASE WHEN note <> ''
        THEN jsonb_build_array(jsonb_build_object('type', 'paragraph', 'text', note))
        ELSE '[]'::jsonb END
    || CASE WHEN footer <> ''
        THEN jsonb_build_array(jsonb_build_object('type', 'footer', 'text', footer))
        ELSE '[]'::jsonb END;

ALTER TABLE org_email_templates ALTER COLUMN blocks SET NOT NULL;

ALTER TABLE org_email_templates
    DROP COLUMN headline,
    DROP COLUMN paragraphs,
    DROP COLUMN cta_label,
    DROP COLUMN cta_url,
    DROP COLUMN link_fallback,
    DROP COLUMN note,
    DROP COLUMN footer;

-- +goose Down
-- Best effort: the fixed shape cannot carry an arbitrary layout, so the first
-- heading, the first button and the first footer are kept, every paragraph is
-- kept in order, and dividers (and any block order beyond the fixed shape) are
-- dropped.
ALTER TABLE org_email_templates
    ADD COLUMN headline TEXT NOT NULL DEFAULT '',
    ADD COLUMN paragraphs TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN cta_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN cta_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN link_fallback TEXT NOT NULL DEFAULT '',
    ADD COLUMN note TEXT NOT NULL DEFAULT '',
    ADD COLUMN footer TEXT NOT NULL DEFAULT '';

UPDATE org_email_templates SET
    headline = COALESCE(
        (SELECT e.b ->> 'text' FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
         WHERE e.b ->> 'type' = 'heading' ORDER BY e.ord LIMIT 1), ''),
    paragraphs = COALESCE(
        (SELECT array_agg(e.b ->> 'text' ORDER BY e.ord) FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
         WHERE e.b ->> 'type' = 'paragraph'), '{}'),
    cta_label = COALESCE(
        (SELECT e.b ->> 'label' FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
         WHERE e.b ->> 'type' = 'button' ORDER BY e.ord LIMIT 1), ''),
    cta_url = COALESCE(
        (SELECT e.b ->> 'url' FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
         WHERE e.b ->> 'type' = 'button' ORDER BY e.ord LIMIT 1), ''),
    link_fallback = COALESCE(
        (SELECT e.b ->> 'linkFallback' FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
         WHERE e.b ->> 'type' = 'button' ORDER BY e.ord LIMIT 1), ''),
    footer = COALESCE(
        (SELECT e.b ->> 'text' FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
         WHERE e.b ->> 'type' = 'footer' ORDER BY e.ord LIMIT 1), '');

ALTER TABLE org_email_templates DROP COLUMN blocks;
