BEGIN;

ALTER TABLE cells
    ADD COLUMN route_origin text;

ALTER TABLE cells
    ADD CONSTRAINT cells_route_origin_safe
    CHECK (
        route_origin IS NULL OR (
            length(route_origin) BETWEEN 1 AND 2048
            AND route_origin = btrim(route_origin)
            AND route_origin !~ '[[:cntrl:]]'
        )
    );

COMMIT;
