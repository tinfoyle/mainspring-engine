BEGIN;

ALTER TABLE sessions
    ADD COLUMN authentication_method text NOT NULL DEFAULT 'password',
    ADD COLUMN reauthentication_method text NOT NULL DEFAULT 'password';

ALTER TABLE sessions
    ADD CONSTRAINT sessions_authentication_method_check CHECK (authentication_method IN ('password','passkey')),
    ADD CONSTRAINT sessions_reauthentication_method_check CHECK (reauthentication_method IN ('password','passkey')),
    ALTER COLUMN authentication_method DROP DEFAULT,
    ALTER COLUMN reauthentication_method DROP DEFAULT;

COMMIT;
