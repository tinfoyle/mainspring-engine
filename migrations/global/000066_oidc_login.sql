BEGIN;

ALTER TABLE sessions DROP CONSTRAINT sessions_authentication_method_check;
ALTER TABLE sessions DROP CONSTRAINT sessions_reauthentication_method_check;

ALTER TABLE sessions
    ADD CONSTRAINT sessions_authentication_method_check CHECK (authentication_method IN ('password','passkey','oidc')),
    ADD CONSTRAINT sessions_reauthentication_method_check CHECK (reauthentication_method IN ('password','passkey','oidc'));

COMMIT;
