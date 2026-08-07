-- Spring Authorization Server schema (registered clients), MySQL-adapted.
-- The authorization server uses JdbcRegisteredClientRepository, which needs this
-- table to exist BEFORE startup (JPA ddl-auto:update does not create it). Mounted
-- into the shared MySQL's /docker-entrypoint-initdb.d so it runs on first init.
-- Canonical source: spring-authorization-server oauth2-registered-client-schema.sql
-- (timestamp -> datetime for MySQL portability).
CREATE TABLE IF NOT EXISTS oauth2_registered_client (
    id VARCHAR(100) NOT NULL,
    client_id VARCHAR(100) NOT NULL,
    client_id_issued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    client_secret VARCHAR(200) DEFAULT NULL,
    client_secret_expires_at DATETIME DEFAULT NULL,
    client_name VARCHAR(200) NOT NULL,
    client_authentication_methods VARCHAR(1000) NOT NULL,
    authorization_grant_types VARCHAR(1000) NOT NULL,
    redirect_uris VARCHAR(1000) DEFAULT NULL,
    post_logout_redirect_uris VARCHAR(1000) DEFAULT NULL,
    scopes VARCHAR(1000) NOT NULL,
    client_settings VARCHAR(2000) NOT NULL,
    token_settings VARCHAR(2000) NOT NULL,
    PRIMARY KEY (id)
);
