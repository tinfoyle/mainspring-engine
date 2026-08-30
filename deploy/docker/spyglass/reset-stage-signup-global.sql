\set ON_ERROR_STOP on

BEGIN;

CREATE TEMP TABLE stage_signup_reset_target (
    user_id uuid PRIMARY KEY,
    account_id uuid NOT NULL UNIQUE,
    primary_email text NOT NULL,
    cell_id text NOT NULL,
    placement_generation bigint NOT NULL,
    execute_reset boolean NOT NULL
) ON COMMIT DROP;

INSERT INTO stage_signup_reset_target
    (user_id,account_id,primary_email,cell_id,placement_generation,execute_reset)
SELECT u.id,a.id,u.primary_email,a.cell_id,a.placement_generation,:'execute_reset'::boolean
FROM users u
JOIN accounts a ON a.created_by_user_id=u.id
WHERE u.primary_email=lower(btrim(:'target_email'))
  AND a.id=:'target_account_id'::uuid
  AND a.cell_id=:'target_cell_id'
  AND a.placement_generation=:'placement_generation'::bigint;

DO $guard$
DECLARE
    target stage_signup_reset_target%ROWTYPE;
    row_count bigint;
    relation record;
    login_hash bytea;
    reauthentication_hash bytea;
    privacy_subject_id uuid;
BEGIN
    SELECT * INTO target FROM stage_signup_reset_target;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002',
            MESSAGE='Stage signup reset refused: exact User and Account were not found';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:stage-signup-reset:' || target.account_id::text,0));
    PERFORM 1 FROM users WHERE id=target.user_id FOR UPDATE;
    PERFORM 1 FROM accounts WHERE id=target.account_id FOR UPDATE;

    SELECT count(*) INTO row_count
    FROM users
    WHERE id=target.user_id AND primary_email=target.primary_email
      AND state='active' AND email_verified_at IS NOT NULL;
    IF row_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: User is not one active verified identity';
    END IF;

    SELECT count(*) INTO row_count
    FROM accounts
    WHERE id=target.account_id AND created_by_user_id=target.user_id
      AND account_type='inactive' AND state='active'
      AND cell_id=target.cell_id AND placement_generation=target.placement_generation;
    IF row_count<>1 OR (SELECT count(*) FROM accounts WHERE created_by_user_id=target.user_id)<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: Account is not the User''s sole inactive signup shell';
    END IF;

    SELECT count(*) INTO row_count
    FROM memberships
    WHERE account_id=target.account_id AND user_id=target.user_id
      AND role='owner' AND state='active';
    IF row_count<>1 OR
       (SELECT count(*) FROM memberships WHERE account_id=target.account_id)<>1 OR
       (SELECT count(*) FROM memberships WHERE user_id=target.user_id)<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: sole active owner membership invariant failed';
    END IF;

    SELECT count(*) INTO row_count
    FROM account_directory
    WHERE account_id=target.account_id AND cell_id=target.cell_id
      AND placement_generation=target.placement_generation AND state='active';
    IF row_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: active Account directory invariant failed';
    END IF;

    SELECT count(*) INTO row_count
    FROM account_cell_provision_queue
    WHERE account_id=target.account_id AND cell_id=target.cell_id
      AND placement_generation=target.placement_generation AND processing_state='completed';
    IF row_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: completed cell provisioning invariant failed';
    END IF;

    SELECT count(*) INTO row_count
    FROM entitlement_snapshots
    WHERE account_id=target.account_id
      AND jsonb_typeof(effective_packages)='array'
      AND jsonb_array_length(effective_packages)=0;
    IF row_count<>1 OR (SELECT count(*) FROM entitlement_snapshots WHERE account_id=target.account_id)<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: Account does not have exactly one empty entitlement snapshot';
    END IF;

    IF (SELECT count(*) FROM authentication_identities WHERE user_id=target.user_id)<1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: User has no authentication identity';
    END IF;

    -- Any direct Account reference outside the creation shell is evidence that
    -- the signup advanced into billing, product, support, movement or deletion.
    FOR relation IN
        SELECT constraint_row.conrelid,
               namespace.nspname AS table_schema,
               relation_row.relname AS table_name,
               attribute_row.attname AS column_name,
               array_length(constraint_row.conkey,1) AS key_columns
        FROM pg_constraint constraint_row
        JOIN pg_class relation_row ON relation_row.oid=constraint_row.conrelid
        JOIN pg_namespace namespace ON namespace.oid=relation_row.relnamespace
        JOIN pg_attribute attribute_row
          ON attribute_row.attrelid=constraint_row.conrelid
         AND attribute_row.attnum=constraint_row.conkey[1]
        WHERE constraint_row.contype='f'
          AND constraint_row.confrelid='public.accounts'::regclass
        ORDER BY namespace.nspname,relation_row.relname,constraint_row.conname
    LOOP
        IF relation.key_columns<>1 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE='Stage signup reset refused: unreviewed compound Account foreign key exists';
        END IF;
        IF relation.table_schema='public' AND relation.table_name IN (
            'account_cell_provision_queue','account_directory','entitlement_snapshots',
            'identity_notification_outbox','memberships'
        ) THEN
            CONTINUE;
        END IF;
        EXECUTE format('SELECT count(*) FROM %I.%I WHERE %I=$1',
            relation.table_schema,relation.table_name,relation.column_name)
        INTO row_count USING target.account_id;
        IF row_count<>0 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE=format('Stage signup reset refused: %I.%I contains %s Account reference(s)',
                    relation.table_schema,relation.table_name,row_count);
        END IF;
    END LOOP;

    -- Catch Account identifiers kept without a foreign key, such as durable
    -- inbox/operator evidence. Only the reviewed creation-shell tables may exist.
    FOR relation IN
        SELECT columns.table_schema,columns.table_name
        FROM information_schema.columns columns
        JOIN information_schema.tables tables
          ON tables.table_schema=columns.table_schema AND tables.table_name=columns.table_name
        WHERE columns.table_schema='public' AND columns.column_name='account_id'
          AND tables.table_type='BASE TABLE'
          AND columns.table_name NOT IN (
              'account_cell_provision_queue','account_directory','entitlement_snapshots',
              'identity_notification_outbox','memberships'
          )
        ORDER BY columns.table_name
    LOOP
        EXECUTE format('SELECT count(*) FROM %I.%I WHERE account_id=$1',relation.table_schema,relation.table_name)
        INTO row_count USING target.account_id;
        IF row_count<>0 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE=format('Stage signup reset refused: %I.%I contains %s Account row(s)',
                    relation.table_schema,relation.table_name,row_count);
        END IF;
    END LOOP;

    -- The User may contain only signup/security/consent material. References
    -- from Affiliate, MCP, Operations, privacy-rights or other domains refuse.
    FOR relation IN
        SELECT constraint_row.conrelid,
               namespace.nspname AS table_schema,
               relation_row.relname AS table_name,
               attribute_row.attname AS column_name,
               array_length(constraint_row.conkey,1) AS key_columns
        FROM pg_constraint constraint_row
        JOIN pg_class relation_row ON relation_row.oid=constraint_row.conrelid
        JOIN pg_namespace namespace ON namespace.oid=relation_row.relnamespace
        JOIN pg_attribute attribute_row
          ON attribute_row.attrelid=constraint_row.conrelid
         AND attribute_row.attnum=constraint_row.conkey[1]
        WHERE constraint_row.contype='f'
          AND constraint_row.confrelid='public.users'::regclass
        ORDER BY namespace.nspname,relation_row.relname,constraint_row.conname
    LOOP
        IF relation.key_columns<>1 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE='Stage signup reset refused: unreviewed compound User foreign key exists';
        END IF;
        IF relation.table_schema='public' AND relation.table_name IN (
            'accounts','authentication_identities','credential_recovery_challenges','memberships',
            'passkey_ceremonies','passkey_users','primary_email_change_challenges',
            'privacy_consent_subjects','sessions','user_recovery_code_sets',
            'user_recovery_codes','user_security_events'
        ) THEN
            CONTINUE;
        END IF;
        EXECUTE format('SELECT count(*) FROM %I.%I WHERE %I=$1',
            relation.table_schema,relation.table_name,relation.column_name)
        INTO row_count USING target.user_id;
        IF row_count<>0 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE=format('Stage signup reset refused: %I.%I contains %s User reference(s)',
                    relation.table_schema,relation.table_name,row_count);
        END IF;
    END LOOP;

    FOR relation IN
        SELECT columns.table_schema,columns.table_name
        FROM information_schema.columns columns
        JOIN information_schema.tables tables
          ON tables.table_schema=columns.table_schema AND tables.table_name=columns.table_name
        WHERE columns.table_schema='public' AND columns.column_name='user_id'
          AND tables.table_type='BASE TABLE'
          AND columns.table_name NOT IN (
              'authentication_identities','credential_recovery_challenges','memberships',
              'passkey_ceremonies','passkey_credentials','passkey_users',
              'primary_email_change_challenges','privacy_consent_subjects','sessions',
              'user_recovery_code_sets','user_recovery_codes','user_security_events'
          )
        ORDER BY columns.table_name
    LOOP
        EXECUTE format('SELECT count(*) FROM %I.%I WHERE user_id=$1',relation.table_schema,relation.table_name)
        INTO row_count USING target.user_id;
        IF row_count<>0 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE=format('Stage signup reset refused: %I.%I contains %s User row(s)',
                    relation.table_schema,relation.table_name,row_count);
        END IF;
    END LOOP;

    IF target.execute_reset THEN
        DELETE FROM registration_challenges
        WHERE primary_email=target.primary_email OR proposed_user_id=target.user_id;
        FOR privacy_subject_id IN
            SELECT id FROM privacy_consent_subjects WHERE user_id=target.user_id ORDER BY id
        LOOP
            -- Consent evidence is immutable except through its explicit
            -- subject-scoped privacy-erasure fence. The transaction-local
            -- setting permits only the subject currently being removed.
            PERFORM set_config('spyglass.privacy_erasure_subject_id',privacy_subject_id::text,true);
            DELETE FROM privacy_consent_subjects WHERE id=privacy_subject_id;
        END LOOP;
        DELETE FROM identity_notification_outbox WHERE account_id=target.account_id;
        DELETE FROM account_cell_provision_queue WHERE account_id=target.account_id;
        DELETE FROM entitlement_snapshots WHERE account_id=target.account_id;
        DELETE FROM memberships WHERE account_id=target.account_id;
        DELETE FROM account_directory WHERE account_id=target.account_id;

        UPDATE cells SET assigned_accounts=assigned_accounts-1
        WHERE id=target.cell_id AND assigned_accounts>0;
        GET DIAGNOSTICS row_count=ROW_COUNT;
        IF row_count<>1 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE='Stage signup reset refused: cell capacity counter could not be decremented';
        END IF;

        DELETE FROM accounts WHERE id=target.account_id;
        GET DIAGNOSTICS row_count=ROW_COUNT;
        IF row_count<>1 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE='Stage signup reset refused: Account changed during deletion';
        END IF;

        login_hash := digest(convert_to(target.primary_email,'UTF8'),'sha256');
        reauthentication_hash := digest(convert_to('reauth:' || target.user_id::text,'UTF8'),'sha256');
        DELETE FROM authentication_rate_limits
        WHERE identifier_hash=login_hash OR identifier_hash=reauthentication_hash;
        DELETE FROM credential_recovery_challenges WHERE user_id=target.user_id;
        DELETE FROM user_security_events WHERE user_id=target.user_id;
        DELETE FROM sessions WHERE user_id=target.user_id;
        DELETE FROM authentication_identities WHERE user_id=target.user_id;
        DELETE FROM users WHERE id=target.user_id;
        GET DIAGNOSTICS row_count=ROW_COUNT;
        IF row_count<>1 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE='Stage signup reset refused: User changed during deletion';
        END IF;
    END IF;
END
$guard$;

SELECT CASE WHEN execute_reset THEN 'global signup fixture deleted' ELSE 'global signup fixture is resettable' END AS result
FROM stage_signup_reset_target;

COMMIT;
