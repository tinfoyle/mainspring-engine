BEGIN;

UPDATE cells
SET route_origin = 'http://app-api.spyglass-reference.svc.cluster.local'
WHERE id = 'cell-us-east-01';

COMMIT;
