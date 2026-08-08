-- Spring Authorization Server schema (authorizations), MySQL-adapted.
-- Canonical source: spring-authorization-server oauth2-authorization-schema.sql, with:
--   * timestamp -> datetime (MySQL portability / avoids the single-auto-timestamp rule)
--   * blob columns carry no DEFAULT (MySQL disallows a DEFAULT on BLOB/TEXT)
-- The user_code_*/device_code_* columns exist in Spring Authorization Server 1.3+;
-- they are included for forward-compatibility and ignored by older row mappers.
CREATE TABLE IF NOT EXISTS oauth2_authorization (
    id VARCHAR(100) NOT NULL,
    registered_client_id VARCHAR(100) NOT NULL,
    principal_name VARCHAR(200) NOT NULL,
    authorization_grant_type VARCHAR(100) NOT NULL,
    authorized_scopes VARCHAR(1000) DEFAULT NULL,
    attributes BLOB,
    state VARCHAR(500) DEFAULT NULL,
    authorization_code_value BLOB,
    authorization_code_issued_at DATETIME DEFAULT NULL,
    authorization_code_expires_at DATETIME DEFAULT NULL,
    authorization_code_metadata BLOB,
    access_token_value BLOB,
    access_token_issued_at DATETIME DEFAULT NULL,
    access_token_expires_at DATETIME DEFAULT NULL,
    access_token_metadata BLOB,
    access_token_type VARCHAR(100) DEFAULT NULL,
    access_token_scopes VARCHAR(1000) DEFAULT NULL,
    oidc_id_token_value BLOB,
    oidc_id_token_issued_at DATETIME DEFAULT NULL,
    oidc_id_token_expires_at DATETIME DEFAULT NULL,
    oidc_id_token_metadata BLOB,
    refresh_token_value BLOB,
    refresh_token_issued_at DATETIME DEFAULT NULL,
    refresh_token_expires_at DATETIME DEFAULT NULL,
    refresh_token_metadata BLOB,
    user_code_value BLOB,
    user_code_issued_at DATETIME DEFAULT NULL,
    user_code_expires_at DATETIME DEFAULT NULL,
    user_code_metadata BLOB,
    device_code_value BLOB,
    device_code_issued_at DATETIME DEFAULT NULL,
    device_code_expires_at DATETIME DEFAULT NULL,
    device_code_metadata BLOB,
    PRIMARY KEY (id)
);
