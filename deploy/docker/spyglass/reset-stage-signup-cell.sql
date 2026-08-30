\set ON_ERROR_STOP on

BEGIN;

CREATE TEMP TABLE stage_signup_reset_target (
    account_id uuid PRIMARY KEY,
    placement_generation bigint NOT NULL,
    execute_reset boolean NOT NULL
) ON COMMIT DROP;

INSERT INTO stage_signup_reset_target (account_id, placement_generation, execute_reset)
VALUES (:'target_account_id'::uuid, :'placement_generation'::bigint, :'execute_reset'::boolean);

DO $guard$
DECLARE
    target stage_signup_reset_target%ROWTYPE;
    row_count bigint;
    relation record;
BEGIN
    SELECT * INTO STRICT target FROM stage_signup_reset_target;

    SELECT count(*) INTO row_count
    FROM spyglass.account_namespaces
    WHERE account_id=target.account_id
      AND placement_generation=target.placement_generation
      AND state='active';
    IF row_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001',
            MESSAGE='Stage signup reset refused: exact active cell namespace was not found';
    END IF;

    FOR relation IN
        SELECT columns.table_schema,columns.table_name
        FROM information_schema.columns columns
        JOIN information_schema.tables tables
          ON tables.table_schema=columns.table_schema AND tables.table_name=columns.table_name
        WHERE columns.column_name='account_id'
          AND tables.table_type='BASE TABLE'
          AND NOT (columns.table_schema='spyglass' AND columns.table_name='account_namespaces')
        ORDER BY columns.table_schema,columns.table_name
    LOOP
        EXECUTE format('SELECT count(*) FROM %I.%I WHERE account_id=$1',relation.table_schema,relation.table_name)
        INTO row_count USING target.account_id;
        IF row_count<>0 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE=format('Stage signup reset refused: cell table %I.%I contains %s Account row(s)',
                    relation.table_schema,relation.table_name,row_count);
        END IF;
    END LOOP;

    IF target.execute_reset THEN
        DELETE FROM spyglass.account_namespaces
        WHERE account_id=target.account_id
          AND placement_generation=target.placement_generation
          AND state='active';
        GET DIAGNOSTICS row_count=ROW_COUNT;
        IF row_count<>1 THEN
            RAISE EXCEPTION USING ERRCODE='P0001',
                MESSAGE='Stage signup reset refused: cell namespace changed during deletion';
        END IF;
    END IF;
END
$guard$;

SELECT CASE WHEN execute_reset THEN 'cell namespace deleted' ELSE 'cell namespace is resettable' END AS result
FROM stage_signup_reset_target;

COMMIT;
