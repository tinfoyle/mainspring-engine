CREATE TABLE conversation_documents (
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    document_name TEXT NOT NULL,
    attached_by UUID REFERENCES users(id) ON DELETE SET NULL,
    attached_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, document_id)
);

CREATE TABLE boardroom_run_documents (
    run_id UUID NOT NULL REFERENCES boardroom_runs(id) ON DELETE CASCADE,
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    document_name TEXT NOT NULL,
    attached_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (run_id, document_id)
);

CREATE INDEX boardroom_run_documents_document_idx
    ON boardroom_run_documents(document_id, run_id);
