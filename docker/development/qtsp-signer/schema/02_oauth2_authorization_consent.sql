-- Spring Authorization Server schema (authorization consent), MySQL-adapted.
-- Canonical source: spring-authorization-server oauth2-authorization-consent-schema.sql
CREATE TABLE IF NOT EXISTS oauth2_authorization_consent (
    registered_client_id VARCHAR(100) NOT NULL,
    principal_name VARCHAR(200) NOT NULL,
    authorities VARCHAR(1000) NOT NULL,
    PRIMARY KEY (registered_client_id, principal_name)
);
