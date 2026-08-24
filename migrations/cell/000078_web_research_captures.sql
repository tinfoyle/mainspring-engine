BEGIN;

-- Source bytes live in the ordinary immutable Knowledge object/revision
-- lifecycle. This row freezes public source attribution and the exact
-- connection authority used to retrieve it; provider credentials remain
-- non-portable broker references and are omitted from Account export.
ALTER TABLE spyglass.knowledge_document_revisions
  ADD CONSTRAINT knowledge_document_revisions_document_identity UNIQUE (account_id,document_id,id);

CREATE TABLE spyglass.integration_web_research_captures (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_revision_id uuid NOT NULL,
    connection_revision bigint NOT NULL CHECK (connection_revision>0),
    credential_id uuid NOT NULL,
    credential_generation bigint NOT NULL CHECK (credential_generation>0),
    requested_url_sha256 bytea NOT NULL CHECK (octet_length(requested_url_sha256)=32),
    canonical_url text NOT NULL CHECK (octet_length(canonical_url) BETWEEN 9 AND 2048 AND canonical_url=btrim(canonical_url) AND canonical_url ~ '^https://'),
    title text NOT NULL CHECK (octet_length(title) BETWEEN 1 AND 200 AND title=btrim(title)),
    excerpt text NOT NULL CHECK (octet_length(excerpt)<=12000 AND excerpt=btrim(excerpt)),
    media_type text NOT NULL CHECK (media_type IN ('text/html','text/plain','application/pdf')),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    document_id uuid NOT NULL,
    document_revision_id uuid NOT NULL,
    retrieved_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL CHECK (recorded_at>=retrieved_at),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (octet_length(created_by_id) BETWEEN 1 AND 256 AND created_by_id=btrim(created_by_id)),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,connection_id,connection_revision_id,connection_revision)
      REFERENCES spyglass.integration_connection_revisions(account_id,connection_id,id,revision) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,credential_id,credential_generation)
      REFERENCES spyglass.integration_credentials(account_id,connection_id,id,generation) ON DELETE CASCADE,
    FOREIGN KEY (account_id,document_id) REFERENCES spyglass.knowledge_documents(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,document_id,document_revision_id)
      REFERENCES spyglass.knowledge_document_revisions(account_id,document_id,id) ON DELETE CASCADE
);

CREATE INDEX integration_web_research_captures_source
  ON spyglass.integration_web_research_captures(account_id,connection_id,retrieved_at DESC,id);

ALTER TABLE spyglass.integration_web_research_captures ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_web_research_captures FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_web_research_captures_isolation ON spyglass.integration_web_research_captures
  USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
  WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE TRIGGER integration_web_research_captures_immutable BEFORE UPDATE OR DELETE
  ON spyglass.integration_web_research_captures FOR EACH ROW EXECUTE FUNCTION spyglass.reject_integration_immutable_change();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE
  ON spyglass.integration_web_research_captures FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER integration_web_research_captures_erasure_count BEFORE DELETE
  ON spyglass.integration_web_research_captures FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();

REVOKE ALL ON TABLE spyglass.integration_web_research_captures FROM PUBLIC;

COMMIT;
